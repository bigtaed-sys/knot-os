package notify

import (
	"fmt"
	"strings"
)

// L10n holds a phrase dictionary per language. Phrases are looked
// up by stable string keys; missing keys fall back to the English
// dictionary, then to the key itself, so a typo in a handler
// surfaces instead of crashing.
//
// Phrase values can be Go format strings; T() accepts variadic
// args and runs fmt.Sprintf when args are non-empty.
type L10n struct {
	primary string
	tables  map[string]map[string]string
}

// newL10n constructs the bot's translation tables. Compact: every
// phrase the bot ever shows lives in this one file, in both
// languages, side-by-side. Adding a phrase = one entry in each
// map, no string-extraction tooling needed at v0.4 scale.
func newL10n(primary string) *L10n {
	if primary != "ru" && primary != "en" {
		primary = "ru"
	}
	return &L10n{
		primary: primary,
		tables: map[string]map[string]string{
			"ru": phrasesRU,
			"en": phrasesEN,
		},
	}
}

// T returns the localized phrase for key in the given language.
// Falls back to English, then the key itself, so a missing
// translation surfaces as a string the developer can grep for.
func (l *L10n) T(lang, key string, args ...any) string {
	if lang == "" {
		lang = l.primary
	}
	tab, ok := l.tables[lang]
	if !ok {
		tab = l.tables["en"]
	}
	val, ok := tab[key]
	if !ok {
		// English fallback before giving up.
		if lang != "en" {
			if alt, ok := l.tables["en"][key]; ok {
				val = alt
			}
		}
	}
	if val == "" {
		val = key
	}
	if len(args) == 0 {
		return val
	}
	return fmt.Sprintf(val, args...)
}

// Joined renders a single string with one phrase per line. Used
// by the multi-line status messages where each line is its own
// translation key.
func (l *L10n) Joined(lang string, keys ...string) string {
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, l.T(lang, k))
	}
	return strings.Join(parts, "\n")
}

var phrasesRU = map[string]string{
	"welcome":          "Привет! Это бот KnotOS — управление вашим роутером прямо из Telegram.",
	"need_pin":         "Чтобы привязать этот чат, откройте админку → Система → Уведомления, нажмите «Привязать Telegram», и пришлите мне 6-значный PIN.",
	"pin_ok":           "Чат успешно привязан, %s. Команда /help — список действий.",
	"pin_bad":          "PIN не совпадает или истёк. Откройте админку и сгенерируйте новый.",
	"unauth":           "Этот чат не привязан. Откройте админку → Система → Уведомления, чтобы добавить.",
	"help":             "Команды:\n/status — текущее состояние\n/devices — список устройств\n/protection — DNS-статистика\n/guest — гостевая сеть\n/lang — язык бота\n/unlink — отвязать этот чат",
	"unknown_command":  "Не понял команду. /help покажет что я умею.",
	"choose_lang":      "Выберите язык:",
	"lang_set":         "Язык: русский ✓",
	"unlink_done":      "Чат отвязан. Чтобы вернуться — снова сгенерируйте PIN в админке.",

	// Status
	"status_title":   "📊 *Состояние KnotOS*",
	"status_role":    "Роль: %s",
	"status_device":  "Устройство: %s",
	"status_version": "Версия: %s",
	"status_wan":     "WAN: %s",
	"status_ap":      "AP: %s (%s)",
	"status_clients": "Онлайн устройств: %d",
	"role_setup":     "режим настройки",
	"role_extender": "Wi-Fi повторитель",
	"role_router":   "Wi-Fi роутер",
	"up":             "🟢 работает",
	"down":           "🔴 нет связи",

	// Devices list
	"devices_title":     "🖥 *Устройства*",
	"devices_empty":     "Пока нет устройств в реестре.",
	"device_online":     "🟢",
	"device_offline":    "⚫",
	"device_stale":      "🕓",
	"device_menu_title": "*%s*\nMAC: `%s`\nIP: %s\nПрофиль: %s",
	"profile_none":      "—",
	"button_back":       "« Назад",
	"button_wake":       "🔌 Разбудить",
	"button_set_profile": "🛡 Сменить профиль",
	"button_pick":       "✓ Выбрать",
	"wake_sent":         "Magic-пакет отправлен. Если WoL включён в BIOS, устройство проснётся через несколько секунд.",
	"wake_offline_only": "Будить можно только устройства, которые сейчас не онлайн.",
	"profile_picker":    "Выберите профиль для %s:",
	"profile_set_ok":    "Профиль изменён на %s.",

	// Protection
	"protection_title":   "🛡 *Защита (DNS)*",
	"protection_queries": "Запросов всего: %d",
	"protection_blocked": "Заблокировано: %d (%s%%)",
	"protection_mode":    "Транспорт: %s",
	"upstream_udp":       "UDP (RFC 1035)",
	"upstream_doh":       "DoH (HTTPS)",
	"button_dns_doh":     "→ Переключить на DoH",
	"button_dns_udp":     "→ Переключить на UDP",
	"dns_switched":       "Транспорт DNS: %s",

	// Guest
	"guest_title":       "👥 *Гостевая сеть*",
	"guest_none":        "Гостевая сеть не активна.",
	"guest_active":      "SSID: `%s`\nПароль: `%s`\nОсталось: %s",
	"guest_forever":     "до отмены",
	"button_guest_revoke": "✗ Закрыть сейчас",
	"guest_revoked":     "Гостевая сеть закрыта.",

	// Notification messages (push from event bus)
	"notif_wan_up":             "🟢 *WAN восстановлен* — %s (%s)",
	"notif_wan_down":           "🔴 *WAN потерян* — %s",
	"notif_device_joined":      "➕ *Новое устройство*: %s (`%s`, %s)",
	"notif_guest_created":      "👥 Гостевая сеть `%s` создана.",
	"notif_guest_revoked":      "👥 Гостевая сеть `%s` закрыта.",
	"notif_guest_expired":      "👥 Гостевая сеть `%s` истекла автоматически.",
	"notif_update_available":   "⬆️ Доступно обновление KnotOS: %s → %s. Откройте админку → Система → Обновления, чтобы установить.",
	"notif_profile_changed":    "🛡 Профиль `%s` присвоен устройству %s (было: %s).",
}

var phrasesEN = map[string]string{
	"welcome":          "Hi! This is the KnotOS bot — control your router right from Telegram.",
	"need_pin":         "To link this chat, open the admin UI → System → Notifications, click \"Link Telegram\" and send me the 6-digit PIN.",
	"pin_ok":           "Chat linked, %s. /help lists what I can do.",
	"pin_bad":          "PIN doesn't match or has expired. Generate a new one in the admin UI.",
	"unauth":           "This chat isn't linked. Open the admin UI → System → Notifications to add it.",
	"help":             "Commands:\n/status — current state\n/devices — device list\n/protection — DNS stats\n/guest — guest network\n/lang — bot language\n/unlink — disconnect this chat",
	"unknown_command":  "Unknown command. /help shows what I can do.",
	"choose_lang":      "Pick a language:",
	"lang_set":         "Language: English ✓",
	"unlink_done":      "Chat unlinked. Generate a fresh PIN in the admin UI to come back.",

	// Status
	"status_title":   "📊 *KnotOS status*",
	"status_role":    "Role: %s",
	"status_device":  "Device: %s",
	"status_version": "Version: %s",
	"status_wan":     "WAN: %s",
	"status_ap":      "AP: %s (%s)",
	"status_clients": "Devices online: %d",
	"role_setup":     "setup mode",
	"role_extender": "Wi-Fi extender",
	"role_router":   "Wi-Fi router",
	"up":             "🟢 up",
	"down":           "🔴 down",

	// Devices list
	"devices_title":     "🖥 *Devices*",
	"devices_empty":     "No devices in the registry yet.",
	"device_online":     "🟢",
	"device_offline":    "⚫",
	"device_stale":      "🕓",
	"device_menu_title": "*%s*\nMAC: `%s`\nIP: %s\nProfile: %s",
	"profile_none":      "—",
	"button_back":       "« Back",
	"button_wake":       "🔌 Wake",
	"button_set_profile": "🛡 Change profile",
	"button_pick":       "✓ Pick",
	"wake_sent":         "Magic packet sent. If WoL is enabled in BIOS, the device wakes within seconds.",
	"wake_offline_only": "Only currently-offline devices can be woken.",
	"profile_picker":    "Pick a profile for %s:",
	"profile_set_ok":    "Profile set to %s.",

	// Protection
	"protection_title":   "🛡 *Protection (DNS)*",
	"protection_queries": "Total queries: %d",
	"protection_blocked": "Blocked: %d (%s%%)",
	"protection_mode":    "Transport: %s",
	"upstream_udp":       "UDP (RFC 1035)",
	"upstream_doh":       "DoH (HTTPS)",
	"button_dns_doh":     "→ Switch to DoH",
	"button_dns_udp":     "→ Switch to UDP",
	"dns_switched":       "DNS transport: %s",

	// Guest
	"guest_title":       "👥 *Guest network*",
	"guest_none":        "No guest network is active.",
	"guest_active":      "SSID: `%s`\nPSK: `%s`\nRemaining: %s",
	"guest_forever":     "until revoked",
	"button_guest_revoke": "✗ Revoke now",
	"guest_revoked":     "Guest network closed.",

	// Notification messages (push from event bus)
	"notif_wan_up":             "🟢 *WAN restored* — %s (%s)",
	"notif_wan_down":           "🔴 *WAN lost* — %s",
	"notif_device_joined":      "➕ *New device*: %s (`%s`, %s)",
	"notif_guest_created":      "👥 Guest network `%s` created.",
	"notif_guest_revoked":      "👥 Guest network `%s` closed.",
	"notif_guest_expired":      "👥 Guest network `%s` expired automatically.",
	"notif_update_available":   "⬆️ KnotOS update available: %s → %s. Open admin UI → System → Updates to install.",
	"notif_profile_changed":    "🛡 Profile `%s` assigned to %s (was: %s).",
}
