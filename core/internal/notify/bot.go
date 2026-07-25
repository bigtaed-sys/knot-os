package notify

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/knot-os/knot-os/core/internal/events"
)

// Bot is the long-polling Telegram bot. Holds the HTTP client,
// the persistent state, and the data-source callbacks the bot
// uses when answering /status, /devices, /protection, /guest.
//
// Constructed lazily once the user has set a token: a token-less
// Store keeps the bot idle (Run returns immediately).
type Bot struct {
	store  *Store
	logger *log.Logger
	bus    *events.Bus
	tr     *L10n

	// Data-source callbacks. main.go wires these up so the bot
	// has read access to live state without owning it.
	StatusFn   func() StatusSnapshot
	DevicesFn  func() []DeviceSnapshot
	ProfilesFn func() []ProfileSnapshot
	WakeFn     func(mac string) error
	SetProfileFn func(mac, profileID string) error
	ProtectionFn func() ProtectionSnapshot
	SetDNSModeFn func(mode string) error
	GuestFn      func() *GuestSnapshot
	RevokeGuestFn func() error
	RoutingFn    func() *RoutingSnapshot

	mu       sync.Mutex
	running  bool
	cancelFn context.CancelFunc
	client   *telegramClient
	subID    uint64
}

// StatusSnapshot is what /status renders. main.go fills this from
// the live config + backend + device registry.
type StatusSnapshot struct {
	Role           string // "setup" | "wifi-extender" | "wifi-router"
	DeviceName     string
	Version        string
	WANUp          bool
	WANIface       string
	WANIP          string
	APSSID         string
	APUp           bool
	OnlineDevices  int
}

// DeviceSnapshot is one row in /devices.
type DeviceSnapshot struct {
	MAC       string
	Label     string
	IP        string
	Online    bool
	Stale     bool
	ProfileID string
}

// ProfileSnapshot is one entry the profile-picker callback uses.
type ProfileSnapshot struct {
	ID   string
	Name string
}

// ProtectionSnapshot is what /protection renders.
type ProtectionSnapshot struct {
	Queries      uint64
	Blocked      uint64
	BlockedRatio float64 // 0..1
	UpstreamMode string  // "udp" | "doh"
}

// GuestSnapshot is the active guest session, if any.
type GuestSnapshot struct {
	SSID         string
	PSK          string
	RemainingSec int64 // -1 means "until revoked"
}

// RoutingSnapshot is what /routing renders. Keeps the bot decoupled
// from the routing package — main.go assembles this from the routing
// result + subscription registry.
type RoutingSnapshot struct {
	// Subscriptions configured (URL-backed + manual). The "manual"
	// pseudo-sub counts as 1 here unless empty.
	Subscriptions int
	// Servers across all subs.
	Servers int
	// DevicesTunnel / DevicesDirect / DevicesKill — bucket counts
	// for the per-device routing decisions.
	DevicesTunnel int
	DevicesDirect int
	DevicesKill   int
	// MissingOutbounds is the same list the UI shows when a profile
	// points at a vanished server. Empty == healthy state.
	MissingOutbounds []string
}

// NewBot builds a Bot with no callbacks wired (caller assigns the
// XxxFn fields). Run does nothing until both the store has a
// token and the bot has been Started.
func NewBot(store *Store, bus *events.Bus, logger *log.Logger) *Bot {
	if logger == nil {
		logger = log.Default()
	}
	primary := store.Snapshot().PrimaryLang
	if primary == "" {
		primary = "ru"
	}
	return &Bot{
		store:  store,
		logger: logger,
		bus:    bus,
		tr:     newL10n(primary),
	}
}

// Start launches the long-polling loop and the event-bus
// subscriber in goroutines. Idempotent: a second call after the
// bot is already running is a no-op.
//
// Returns nil immediately when the store has no token — the bot
// silently waits for the user to configure one (Restart() picks
// up a fresh token without process restart).
func (b *Bot) Start(parent context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.running {
		return nil
	}
	st := b.store.Snapshot()
	if st.BotToken == "" {
		b.logger.Printf("notify: bot token not configured; bot loop is idle")
		return nil
	}
	b.client = newTelegramClient(st.BotToken)

	// Verify the token by calling /getMe; cache the resulting
	// username for the UI link button.
	verifyCtx, cancelVerify := context.WithTimeout(parent, 10*time.Second)
	me, err := b.client.GetMe(verifyCtx)
	cancelVerify()
	if err != nil {
		b.logger.Printf("notify: getMe failed (token bad?): %v", err)
		return err
	}
	b.store.SetBotUsername(me.Username)
	b.logger.Printf("notify: bot @%s (id=%d) ready", me.Username, me.ID)

	ctx, cancel := context.WithCancel(parent)
	b.cancelFn = cancel
	b.running = true

	go b.pollLoop(ctx)
	if b.bus != nil {
		b.subID = b.bus.Subscribe(b.onEvent)
	}
	return nil
}

// Stop terminates the poll loop + unsubscribes from the event
// bus. Safe to call multiple times.
func (b *Bot) Stop() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return
	}
	if b.cancelFn != nil {
		b.cancelFn()
	}
	if b.bus != nil && b.subID != 0 {
		b.bus.Unsubscribe(b.subID)
	}
	b.running = false
}

// Restart picks up a freshly-set token. main.go calls this from
// the API handler that updates the token.
func (b *Bot) Restart(parent context.Context) error {
	b.Stop()
	return b.Start(parent)
}

// --- long-polling loop -----------------------------------------------------

func (b *Bot) pollLoop(ctx context.Context) {
	var offset int64
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		updates, err := b.client.GetUpdates(ctx, offset, 25)
		if err != nil {
			// Telegram occasionally hiccups; back off briefly and
			// keep going. Ctx cancellation also surfaces here.
			if ctx.Err() != nil {
				return
			}
			b.logger.Printf("notify: getUpdates: %v (retrying)", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.handleUpdate(ctx, u)
		}
	}
}

func (b *Bot) handleUpdate(ctx context.Context, u Update) {
	defer func() {
		if r := recover(); r != nil {
			b.logger.Printf("notify: panic in handleUpdate: %v", r)
		}
	}()
	switch {
	case u.Message != nil:
		b.handleMessage(ctx, u.Message)
	case u.CallbackQuery != nil:
		b.handleCallback(ctx, u.CallbackQuery)
	}
}

// handleMessage dispatches /commands and free-text PINs.
func (b *Bot) handleMessage(ctx context.Context, m *Message) {
	if m == nil || m.Chat == nil || m.From == nil {
		return
	}
	chatID := m.Chat.ID
	text := strings.TrimSpace(m.Text)

	// Linking: a not-yet-linked chat sends either /start (we send
	// the welcome / "send your PIN") or a 6-digit PIN.
	if !b.store.IsLinked(chatID) {
		switch {
		case text == "/start" || text == "/help":
			lang := b.store.Snapshot().PrimaryLang
			b.send(ctx, chatID, b.tr.T(lang, "welcome")+"\n\n"+b.tr.T(lang, "need_pin"), nil)
		case isPIN(text):
			link, err := b.store.ClaimPIN(text, chatID, m.From.Username, m.From.FirstName, m.From.LastName)
			if err != nil {
				b.send(ctx, chatID, b.tr.T(b.store.Snapshot().PrimaryLang, "pin_bad"), nil)
				return
			}
			b.send(ctx, chatID, b.tr.T(link.Lang, "pin_ok", displayName(link)), nil)
		default:
			b.send(ctx, chatID, b.tr.T(b.store.Snapshot().PrimaryLang, "unauth"), nil)
		}
		return
	}

	lang := b.store.LangFor(chatID)
	switch {
	case text == "/start" || text == "/help":
		b.send(ctx, chatID, b.tr.T(lang, "help"), nil)
	case text == "/status":
		b.sendStatus(ctx, chatID, lang)
	case text == "/devices":
		b.sendDeviceList(ctx, chatID, lang, 0)
	case text == "/protection":
		b.sendProtection(ctx, chatID, lang)
	case text == "/guest":
		b.sendGuest(ctx, chatID, lang)
	case text == "/routing":
		b.sendRouting(ctx, chatID, lang)
	case text == "/tips":
		b.send(ctx, chatID, b.tr.T(lang, "tips"), nil)
	case text == "/lang":
		b.send(ctx, chatID, b.tr.T(lang, "choose_lang"), &InlineKeyboard{
			{
				{Text: "🇷🇺 Русский", CallbackData: "lang:ru"},
				{Text: "🇬🇧 English", CallbackData: "lang:en"},
			},
		})
	case text == "/unlink":
		_ = b.store.Unlink(chatID)
		b.send(ctx, chatID, b.tr.T(lang, "unlink_done"), nil)
	default:
		b.send(ctx, chatID, b.tr.T(lang, "unknown_command"), nil)
	}
}

// handleCallback dispatches inline-keyboard taps. callback_data
// has the shape `verb:arg1:arg2`; we split on `:` and switch on
// the first token.
func (b *Bot) handleCallback(ctx context.Context, cq *CallbackQuery) {
	if cq == nil || cq.From == nil || cq.Message == nil || cq.Message.Chat == nil {
		return
	}
	chatID := cq.Message.Chat.ID
	if !b.store.IsLinked(chatID) {
		_ = b.client.AnswerCallbackQuery(ctx, AnswerCallbackQueryReq{CallbackQueryID: cq.ID})
		return
	}
	lang := b.store.LangFor(chatID)
	parts := strings.Split(cq.Data, ":")
	verb := parts[0]
	args := parts[1:]

	// Always ack to stop the user's spinner; toast text varies by
	// verb (set later if the action wants to surface a popup).
	ackText := ""

	switch verb {
	case "lang":
		if len(args) >= 1 && (args[0] == "ru" || args[0] == "en") {
			_ = b.store.SetChatLang(chatID, args[0])
			lang = args[0]
			b.editText(ctx, chatID, cq.Message.MessageID, b.tr.T(lang, "lang_set"), nil)
		}
	case "status":
		b.editStatus(ctx, chatID, cq.Message.MessageID, lang)
	case "devices":
		b.editDeviceList(ctx, chatID, cq.Message.MessageID, lang)
	case "device":
		if len(args) >= 1 {
			b.editDeviceMenu(ctx, chatID, cq.Message.MessageID, lang, args[0])
		}
	case "wake":
		if len(args) >= 1 && b.WakeFn != nil {
			if err := b.WakeFn(args[0]); err != nil {
				ackText = err.Error()
			} else {
				ackText = b.tr.T(lang, "wake_sent")
			}
		}
	case "profile":
		// `profile:<mac>` — open picker. `profile:<mac>:<id>` — assign.
		if len(args) == 1 {
			b.editProfilePicker(ctx, chatID, cq.Message.MessageID, lang, args[0])
		} else if len(args) >= 2 && b.SetProfileFn != nil {
			id := args[1]
			if id == "_none_" {
				id = ""
			}
			if err := b.SetProfileFn(args[0], id); err == nil {
				name := id
				if name == "" {
					name = b.tr.T(lang, "profile_none")
				}
				ackText = b.tr.T(lang, "profile_set_ok", name)
				b.editDeviceMenu(ctx, chatID, cq.Message.MessageID, lang, args[0])
			} else {
				ackText = err.Error()
			}
		}
	case "dns":
		// `dns:doh` / `dns:udp`
		if len(args) >= 1 && b.SetDNSModeFn != nil {
			if err := b.SetDNSModeFn(args[0]); err != nil {
				ackText = err.Error()
			} else {
				name := b.tr.T(lang, "upstream_udp")
				if args[0] == "doh" {
					name = b.tr.T(lang, "upstream_doh")
				}
				ackText = b.tr.T(lang, "dns_switched", name)
				b.editProtection(ctx, chatID, cq.Message.MessageID, lang)
			}
		}
	case "guest":
		if len(args) >= 1 && args[0] == "revoke" && b.RevokeGuestFn != nil {
			if err := b.RevokeGuestFn(); err != nil {
				ackText = err.Error()
			} else {
				ackText = b.tr.T(lang, "guest_revoked")
				b.editGuest(ctx, chatID, cq.Message.MessageID, lang)
			}
		}
	}

	_ = b.client.AnswerCallbackQuery(ctx, AnswerCallbackQueryReq{
		CallbackQueryID: cq.ID,
		Text:            ackText,
		ShowAlert:       len(ackText) > 30, // long messages get a modal
	})
}

// --- per-command renderers ------------------------------------------------

func (b *Bot) sendStatus(ctx context.Context, chatID int64, lang string) {
	text, kb := b.renderStatus(lang)
	b.send(ctx, chatID, text, kb)
}

func (b *Bot) editStatus(ctx context.Context, chatID, messageID int64, lang string) {
	text, kb := b.renderStatus(lang)
	b.editText(ctx, chatID, messageID, text, kb)
}

func (b *Bot) renderStatus(lang string) (string, *InlineKeyboard) {
	if b.StatusFn == nil {
		return "—", nil
	}
	s := b.StatusFn()
	roleLabel := b.tr.T(lang, "role_setup")
	switch s.Role {
	case "wifi-extender":
		roleLabel = b.tr.T(lang, "role_extender")
	case "wifi-router":
		roleLabel = b.tr.T(lang, "role_router")
	}
	wan := b.tr.T(lang, "down")
	if s.WANUp {
		wan = b.tr.T(lang, "up") + " (" + s.WANIface + " " + s.WANIP + ")"
	}
	apState := b.tr.T(lang, "down")
	if s.APUp {
		apState = b.tr.T(lang, "up")
	}
	parts := []string{
		b.tr.T(lang, "status_title"),
		"",
		b.tr.T(lang, "status_role", roleLabel),
		b.tr.T(lang, "status_device", s.DeviceName),
		b.tr.T(lang, "status_version", s.Version),
		b.tr.T(lang, "status_wan", wan),
	}
	if s.APSSID != "" {
		parts = append(parts, b.tr.T(lang, "status_ap", s.APSSID, apState))
	}
	parts = append(parts, b.tr.T(lang, "status_clients", s.OnlineDevices))
	return strings.Join(parts, "\n"), nil
}

func (b *Bot) sendDeviceList(ctx context.Context, chatID int64, lang string, _ int) {
	text, kb := b.renderDeviceList(lang)
	b.send(ctx, chatID, text, kb)
}

func (b *Bot) editDeviceList(ctx context.Context, chatID, messageID int64, lang string) {
	text, kb := b.renderDeviceList(lang)
	b.editText(ctx, chatID, messageID, text, kb)
}

func (b *Bot) renderDeviceList(lang string) (string, *InlineKeyboard) {
	if b.DevicesFn == nil {
		return b.tr.T(lang, "devices_empty"), nil
	}
	devs := b.DevicesFn()
	if len(devs) == 0 {
		return b.tr.T(lang, "devices_title") + "\n\n" + b.tr.T(lang, "devices_empty"), nil
	}
	// Each device is one button row; status icon + label in the
	// button text. Tapping opens the device's action menu.
	kb := make(InlineKeyboard, 0, len(devs))
	for i, d := range devs {
		if i >= 12 { // soft cap to keep keyboards readable
			break
		}
		icon := b.tr.T(lang, "device_offline")
		if d.Online {
			icon = b.tr.T(lang, "device_online")
		} else if d.Stale {
			icon = b.tr.T(lang, "device_stale")
		}
		kb = append(kb, []InlineButton{{
			Text:         fmt.Sprintf("%s %s", icon, d.Label),
			CallbackData: "device:" + macKey(d.MAC),
		}})
	}
	return b.tr.T(lang, "devices_title"), &kb
}

func (b *Bot) editDeviceMenu(ctx context.Context, chatID, messageID int64, lang, macK string) {
	mac := unmacKey(macK)
	if b.DevicesFn == nil {
		return
	}
	var d *DeviceSnapshot
	for _, candidate := range b.DevicesFn() {
		if candidate.MAC == mac {
			cp := candidate
			d = &cp
			break
		}
	}
	if d == nil {
		return
	}
	profile := d.ProfileID
	if profile == "" {
		profile = b.tr.T(lang, "profile_none")
	}
	ip := d.IP
	if ip == "" {
		ip = "—"
	}
	text := b.tr.T(lang, "device_menu_title", d.Label, d.MAC, ip, profile)
	kb := InlineKeyboard{}
	if !d.Online {
		kb = append(kb, []InlineButton{{
			Text:         b.tr.T(lang, "button_wake"),
			CallbackData: "wake:" + macK,
		}})
	}
	kb = append(kb,
		[]InlineButton{{
			Text:         b.tr.T(lang, "button_set_profile"),
			CallbackData: "profile:" + macK,
		}},
		[]InlineButton{{
			Text:         b.tr.T(lang, "button_back"),
			CallbackData: "devices",
		}},
	)
	b.editText(ctx, chatID, messageID, text, &kb)
}

func (b *Bot) editProfilePicker(ctx context.Context, chatID, messageID int64, lang, macK string) {
	mac := unmacKey(macK)
	if b.ProfilesFn == nil || b.DevicesFn == nil {
		return
	}
	var label string
	for _, d := range b.DevicesFn() {
		if d.MAC == mac {
			label = d.Label
			break
		}
	}
	profiles := b.ProfilesFn()
	kb := InlineKeyboard{
		{
			{
				Text:         b.tr.T(lang, "profile_none"),
				CallbackData: "profile:" + macK + ":_none_",
			},
		},
	}
	for _, p := range profiles {
		kb = append(kb, []InlineButton{{
			Text:         b.tr.T(lang, "button_pick") + " " + p.Name,
			CallbackData: "profile:" + macK + ":" + p.ID,
		}})
	}
	kb = append(kb, []InlineButton{{
		Text:         b.tr.T(lang, "button_back"),
		CallbackData: "device:" + macK,
	}})
	b.editText(ctx, chatID, messageID, b.tr.T(lang, "profile_picker", label), &kb)
}

func (b *Bot) sendProtection(ctx context.Context, chatID int64, lang string) {
	text, kb := b.renderProtection(lang)
	b.send(ctx, chatID, text, kb)
}

func (b *Bot) editProtection(ctx context.Context, chatID, messageID int64, lang string) {
	text, kb := b.renderProtection(lang)
	b.editText(ctx, chatID, messageID, text, kb)
}

func (b *Bot) renderProtection(lang string) (string, *InlineKeyboard) {
	if b.ProtectionFn == nil {
		return "—", nil
	}
	p := b.ProtectionFn()
	mode := b.tr.T(lang, "upstream_udp")
	switchTo := "doh"
	switchLabel := b.tr.T(lang, "button_dns_doh")
	if p.UpstreamMode == "doh" {
		mode = b.tr.T(lang, "upstream_doh")
		switchTo = "udp"
		switchLabel = b.tr.T(lang, "button_dns_udp")
	}
	pct := fmt.Sprintf("%.1f", p.BlockedRatio*100)
	parts := []string{
		b.tr.T(lang, "protection_title"),
		"",
		b.tr.T(lang, "protection_queries", p.Queries),
		b.tr.T(lang, "protection_blocked", p.Blocked, pct),
		b.tr.T(lang, "protection_mode", mode),
	}
	kb := InlineKeyboard{
		{{Text: switchLabel, CallbackData: "dns:" + switchTo}},
	}
	return strings.Join(parts, "\n"), &kb
}

func (b *Bot) sendGuest(ctx context.Context, chatID int64, lang string) {
	text, kb := b.renderGuest(lang)
	b.send(ctx, chatID, text, kb)
}

func (b *Bot) editGuest(ctx context.Context, chatID, messageID int64, lang string) {
	text, kb := b.renderGuest(lang)
	b.editText(ctx, chatID, messageID, text, kb)
}

func (b *Bot) renderGuest(lang string) (string, *InlineKeyboard) {
	if b.GuestFn == nil {
		return b.tr.T(lang, "guest_title") + "\n\n" + b.tr.T(lang, "guest_none"), nil
	}
	g := b.GuestFn()
	if g == nil {
		return b.tr.T(lang, "guest_title") + "\n\n" + b.tr.T(lang, "guest_none"), nil
	}
	rem := b.tr.T(lang, "guest_forever")
	if g.RemainingSec > 0 {
		rem = formatDuration(time.Duration(g.RemainingSec) * time.Second)
	}
	body := b.tr.T(lang, "guest_active", g.SSID, g.PSK, rem)
	kb := InlineKeyboard{
		{{Text: b.tr.T(lang, "button_guest_revoke"), CallbackData: "guest:revoke"}},
	}
	return b.tr.T(lang, "guest_title") + "\n\n" + body, &kb
}

func (b *Bot) sendRouting(ctx context.Context, chatID int64, lang string) {
	text, _ := b.renderRouting(lang)
	b.send(ctx, chatID, text, nil)
}

func (b *Bot) renderRouting(lang string) (string, *InlineKeyboard) {
	if b.RoutingFn == nil {
		return b.tr.T(lang, "routing_title") + "\n\n" + b.tr.T(lang, "routing_empty"), nil
	}
	r := b.RoutingFn()
	if r == nil || r.Subscriptions == 0 && r.Servers == 0 {
		return b.tr.T(lang, "routing_title") + "\n\n" + b.tr.T(lang, "routing_empty"), nil
	}
	parts := []string{
		b.tr.T(lang, "routing_title"),
		"",
		b.tr.T(lang, "routing_subs", r.Subscriptions, r.Servers),
		b.tr.T(lang, "routing_buckets", r.DevicesTunnel, r.DevicesDirect, r.DevicesKill),
	}
	if len(r.MissingOutbounds) > 0 {
		parts = append(parts, "", b.tr.T(lang, "routing_missing", len(r.MissingOutbounds)))
	}
	return strings.Join(parts, "\n"), nil
}

// --- event-bus subscriber: push notifications -----------------------------

func (b *Bot) onEvent(ctx context.Context, ev events.Event) {
	chats := b.store.AllLinked()
	if len(chats) == 0 {
		return
	}
	for _, c := range chats {
		text := b.formatEvent(ev, c.Lang)
		if text == "" {
			continue
		}
		_ = b.client.SendMessage(ctx, SendMessageReq{
			ChatID:                c.ChatID,
			Text:                  text,
			ParseMode:             "Markdown",
			DisableWebPagePreview: true,
		})
	}
}

func (b *Bot) formatEvent(ev events.Event, lang string) string {
	switch ev.Kind {
	case events.KindWANStatus:
		s, ok := ev.Payload.(events.WANStatus)
		if !ok {
			return ""
		}
		if s.Up {
			return b.tr.T(lang, "notif_wan_up", s.Interface, s.IP)
		}
		return b.tr.T(lang, "notif_wan_down", s.Interface)
	case events.KindDeviceJoined:
		s, ok := ev.Payload.(events.DeviceJoined)
		if !ok {
			return ""
		}
		host := s.Hostname
		if host == "" {
			host = "—"
		}
		ip := s.IP
		if ip == "" {
			ip = "—"
		}
		return b.tr.T(lang, "notif_device_joined", host, s.MAC, ip)
	case events.KindDeviceProfileChanged:
		s, ok := ev.Payload.(events.DeviceProfileChanged)
		if !ok {
			return ""
		}
		old := s.OldProfileID
		if old == "" {
			old = b.tr.T(lang, "profile_none")
		}
		newP := s.NewProfileID
		if newP == "" {
			newP = b.tr.T(lang, "profile_none")
		}
		return b.tr.T(lang, "notif_profile_changed", newP, s.Label, old)
	case events.KindGuestSession:
		s, ok := ev.Payload.(events.GuestSession)
		if !ok {
			return ""
		}
		switch s.Action {
		case "created":
			return b.tr.T(lang, "notif_guest_created", s.SSID)
		case "revoked":
			return b.tr.T(lang, "notif_guest_revoked", s.SSID)
		case "expired":
			return b.tr.T(lang, "notif_guest_expired", s.SSID)
		}
	case events.KindUpdateAvailable:
		s, ok := ev.Payload.(events.UpdateAvailable)
		if !ok {
			return ""
		}
		return b.tr.T(lang, "notif_update_available", s.CurrentVersion, s.LatestVersion)
	case events.KindDataCap:
		s, ok := ev.Payload.(events.DataCap)
		if !ok {
			return ""
		}
		return b.tr.T(lang, "notif_data_cap", humanBytes(s.UsedBytes), humanBytes(s.LimitBytes))
	case events.KindModemFailed:
		s, ok := ev.Payload.(events.ModemFailed)
		if !ok {
			return ""
		}
		return b.tr.T(lang, "notif_modem_failed", s.Reason)
	}
	return ""
}

// humanBytes renders a byte count as "1.2 GB" etc. for notification text.
func humanBytes(n uint64) string {
	const u = 1024.0
	f := float64(n)
	units := []string{"B", "KB", "MB", "GB", "TB"}
	i := 0
	for f >= u && i < len(units)-1 {
		f /= u
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d %s", n, units[i])
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// --- low-level send / edit ------------------------------------------------

func (b *Bot) send(ctx context.Context, chatID int64, text string, kb *InlineKeyboard) {
	if b.client == nil {
		return
	}
	if err := b.client.SendMessage(ctx, SendMessageReq{
		ChatID:                chatID,
		Text:                  text,
		ParseMode:             "Markdown",
		ReplyMarkup:           kb,
		DisableWebPagePreview: true,
	}); err != nil {
		b.logger.Printf("notify: sendMessage to %d: %v", chatID, err)
	}
}

func (b *Bot) editText(ctx context.Context, chatID, messageID int64, text string, kb *InlineKeyboard) {
	if b.client == nil {
		return
	}
	if err := b.client.EditMessageText(ctx, EditMessageReq{
		ChatID:      chatID,
		MessageID:   messageID,
		Text:        text,
		ParseMode:   "Markdown",
		ReplyMarkup: kb,
	}); err != nil {
		// "message is not modified" is a routine result when the
		// content didn't change; suppress in the log.
		if !strings.Contains(err.Error(), "message is not modified") {
			b.logger.Printf("notify: editMessageText to %d/%d: %v", chatID, messageID, err)
		}
	}
}

// --- helpers --------------------------------------------------------------

// macKey encodes a MAC for use in callback_data: drops the colons
// to fit the 64-byte limit and reads back deterministically.
func macKey(mac string) string {
	return strings.ReplaceAll(strings.ToLower(mac), ":", "")
}

// unmacKey is the inverse: 12 hex chars -> "aa:bb:cc:dd:ee:ff".
func unmacKey(s string) string {
	if len(s) != 12 {
		return s
	}
	out := make([]byte, 0, 17)
	for i := 0; i < 12; i++ {
		if i > 0 && i%2 == 0 {
			out = append(out, ':')
		}
		out = append(out, s[i])
	}
	return string(out)
}

func isPIN(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) != 6 {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func displayName(c *LinkedChat) string {
	if c.Username != "" {
		return "@" + c.Username
	}
	if c.FirstName != "" {
		return c.FirstName
	}
	return strconvI64(c.ChatID)
}

func formatDuration(d time.Duration) string {
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	h := int(d / time.Hour)
	m := int((d % time.Hour) / time.Minute)
	if m == 0 {
		return fmt.Sprintf("%dh", h)
	}
	return fmt.Sprintf("%dh %dm", h, m)
}
