package notify

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	tg "github.com/amarnathcjd/gogram/telegram"
)

// mtprotoClient speaks Telegram's native MTProto (via gogram) instead of
// the HTTP Bot API, so the bot keeps working where api.telegram.org is
// blocked — it dials Telegram through the local tg-ws-proxy (WebSocket +
// Cloudflare). It satisfies the same botClient contract as the HTTP
// client, translating to/from the Bot-API-shaped types the bot logic
// already uses, so bot.go is unchanged.
//
// Updates are push-based in gogram; we register handlers that enqueue
// translated Updates, and GetUpdates drains that queue to keep the bot's
// existing long-poll loop working.
//
// EXPERIMENTAL: requires app_id/app_hash (my.telegram.org) and, in a
// blocked network, the local Telegram proxy enabled. Peer resolution
// relies on gogram's cached session.
type mtprotoClient struct {
	client *tg.Client

	mu   sync.Mutex
	q    []Update
	cond *sync.Cond
}

// newMTProtoClient builds and logs in a gogram bot client. proxyURL is a
// tg://proxy link (pointing at the local proxy) or "" for a direct
// connection. sessionPath persists the auth/peer cache across restarts.
func newMTProtoClient(ctx context.Context, appID int32, appHash, token, proxyURL, sessionPath string) (*mtprotoClient, error) {
	cfg := tg.ClientConfig{
		AppID:    appID,
		AppHash:  appHash,
		Session:  sessionPath,
		LogLevel: tg.LogWarn,
	}
	if proxyURL != "" {
		p, err := tg.ProxyFromURL(proxyURL)
		if err != nil {
			return nil, fmt.Errorf("mtproto: proxy url: %w", err)
		}
		cfg.Proxy = p
	}
	client, err := tg.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("mtproto: new client: %w", err)
	}
	if err := client.LoginBot(token); err != nil {
		return nil, fmt.Errorf("mtproto: bot login: %w", err)
	}

	m := &mtprotoClient{client: client}
	m.cond = sync.NewCond(&m.mu)
	m.registerHandlers()
	return m, nil
}

// registerHandlers wires gogram push handlers into the update queue.
func (m *mtprotoClient) registerHandlers() {
	m.client.On(tg.OnMessage, func(msg *tg.NewMessage) error {
		m.enqueue(Update{Message: &Message{
			MessageID: int64(msg.ID),
			Text:      msg.Text(),
			Chat:      &Chat{ID: msg.ChatID(), Type: "private"},
			From:      &User{ID: msg.SenderID()},
		}})
		return nil
	})
	m.client.On(tg.OnCallbackQuery, func(cb *tg.CallbackQuery) error {
		m.enqueue(Update{CallbackQuery: &CallbackQuery{
			ID:   strconv.FormatInt(cb.QueryID, 10),
			Data: cb.DataString(),
			From: &User{ID: cb.GetSenderID()},
			Message: &Message{
				MessageID: int64(cb.MessageID),
				Chat:      &Chat{ID: cb.GetChatID(), Type: "private"},
			},
		}})
		return nil
	})
}

func (m *mtprotoClient) enqueue(u Update) {
	m.mu.Lock()
	m.q = append(m.q, u)
	m.mu.Unlock()
	m.cond.Signal()
}

// GetMe returns the bot's own identity.
func (m *mtprotoClient) GetMe(_ context.Context) (*User, error) {
	me, err := m.client.GetMe()
	if err != nil {
		return nil, err
	}
	return &User{ID: me.ID, IsBot: me.Bot, Username: me.Username, FirstName: me.FirstName}, nil
}

// GetUpdates drains the push queue, blocking up to `timeout` seconds for
// at least one update (mirrors the Bot API long poll). offset is ignored
// — gogram tracks its own update state.
func (m *mtprotoClient) GetUpdates(ctx context.Context, _ int64, timeout int) ([]Update, error) {
	deadline := time.Now().Add(time.Duration(timeout) * time.Second)

	// Wake the waiter if ctx is cancelled or the deadline passes.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		t := time.NewTimer(time.Until(deadline))
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
		case <-stop:
		}
		m.cond.Broadcast()
	}()

	m.mu.Lock()
	defer m.mu.Unlock()
	for len(m.q) == 0 {
		if ctx.Err() != nil || time.Now().After(deadline) {
			return nil, ctx.Err()
		}
		m.cond.Wait()
	}
	out := m.q
	m.q = nil
	return out, nil
}

// SendMessage posts a message (with optional inline keyboard) to a chat.
func (m *mtprotoClient) SendMessage(_ context.Context, req SendMessageReq) error {
	_, err := m.client.SendMessage(req.ChatID, req.Text, m.sendOpts(req.ParseMode, req.ReplyMarkup, req.DisableWebPagePreview))
	return err
}

// EditMessageText edits an existing message in place.
func (m *mtprotoClient) EditMessageText(_ context.Context, req EditMessageReq) error {
	_, err := m.client.EditMessage(req.ChatID, int32(req.MessageID), req.Text, m.sendOpts(req.ParseMode, req.ReplyMarkup, false))
	return err
}

// AnswerCallbackQuery stops the button spinner.
func (m *mtprotoClient) AnswerCallbackQuery(_ context.Context, req AnswerCallbackQueryReq) error {
	qid, err := strconv.ParseInt(req.CallbackQueryID, 10, 64)
	if err != nil {
		return fmt.Errorf("mtproto: bad callback id %q: %w", req.CallbackQueryID, err)
	}
	_, err = m.client.AnswerCallbackQuery(qid, req.Text)
	return err
}

// sendOpts builds gogram send options from the Bot-API-shaped fields.
func (m *mtprotoClient) sendOpts(parseMode string, kb *InlineKeyboard, disablePreview bool) *tg.SendOptions {
	opts := &tg.SendOptions{
		ParseMode:   mapParseMode(parseMode),
		LinkPreview: !disablePreview,
	}
	if markup := buildKeyboard(kb); markup != nil {
		opts.ReplyMarkup = markup
	}
	return opts
}

// mapParseMode translates Bot API parse modes to gogram's.
func mapParseMode(pm string) string {
	switch pm {
	case "Markdown", "MarkdownV2":
		return "markdown"
	case "HTML":
		return "html"
	default:
		return ""
	}
}

// buildKeyboard turns an InlineKeyboard into a gogram ReplyMarkup, or nil.
func buildKeyboard(kb *InlineKeyboard) tg.ReplyMarkup {
	if kb == nil || len(*kb) == 0 {
		return nil
	}
	b := tg.NewKeyboard()
	for _, row := range *kb {
		btns := make([]tg.KeyboardButton, 0, len(row))
		for _, btn := range row {
			if btn.URL != "" {
				btns = append(btns, tg.Button.URL(btn.Text, btn.URL))
			} else {
				btns = append(btns, tg.Button.Data(btn.Text, btn.CallbackData))
			}
		}
		b.AddRow(btns...)
	}
	return b.Build()
}
