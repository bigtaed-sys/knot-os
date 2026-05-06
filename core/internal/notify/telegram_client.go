package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// telegramClient is a tiny Bot API HTTP client. We hand-roll
// because the parts we use (getUpdates, sendMessage with inline
// keyboards, answerCallbackQuery, getMe, editMessageText) are
// each ~10 lines and a third-party library would drag a much
// larger surface area into the dependency graph.
type telegramClient struct {
	token string
	hc    *http.Client
}

func newTelegramClient(token string) *telegramClient {
	return &telegramClient{
		token: token,
		hc: &http.Client{
			// Long polling uses its own timeout per call. The default
			// here is for everything else (sendMessage etc).
			Timeout: 30 * time.Second,
		},
	}
}

// User is the slim shape we store from getMe / from update senders.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Chat is the chat envelope around messages. For a private chat
// (the only kind we authorise) it carries the user's identity too.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Username  string `json:"username,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// Message is the inbound message shape. Trimmed to the fields the
// bot actually inspects.
type Message struct {
	MessageID int64  `json:"message_id"`
	From      *User  `json:"from,omitempty"`
	Chat      *Chat  `json:"chat,omitempty"`
	Date      int64  `json:"date,omitempty"`
	Text      string `json:"text,omitempty"`
}

// CallbackQuery is what the API returns when a user taps an inline
// keyboard button.
type CallbackQuery struct {
	ID      string   `json:"id"`
	From    *User    `json:"from,omitempty"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data,omitempty"`
}

// Update is the long-polling payload.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// InlineButton is one tappable button. CallbackData is what we
// receive back; capped at 64 bytes by Telegram.
type InlineButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboard is the row-of-rows layout the API expects.
type InlineKeyboard [][]InlineButton

// SendMessageReq is the body of /sendMessage.
type SendMessageReq struct {
	ChatID      int64           `json:"chat_id"`
	Text        string          `json:"text"`
	ParseMode   string          `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboard `json:"reply_markup,omitempty"`
	// DisableWebPagePreview keeps notifications from blowing up
	// into giant link previews that hide the actual content.
	DisableWebPagePreview bool `json:"disable_web_page_preview,omitempty"`
}

// EditMessageReq is the body for editing an existing message in
// place (used by inline-keyboard handlers so the user keeps the
// same message thread).
type EditMessageReq struct {
	ChatID      int64           `json:"chat_id"`
	MessageID   int64           `json:"message_id"`
	Text        string          `json:"text"`
	ParseMode   string          `json:"parse_mode,omitempty"`
	ReplyMarkup *InlineKeyboard `json:"reply_markup,omitempty"`
}

// AnswerCallbackQueryReq closes the spinning indicator on the
// inline button + optionally pops a small toast over the chat.
type AnswerCallbackQueryReq struct {
	CallbackQueryID string `json:"callback_query_id"`
	Text            string `json:"text,omitempty"`
	ShowAlert       bool   `json:"show_alert,omitempty"`
}

// apiResponse is the standard envelope for every Bot API call.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description,omitempty"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// call is the shared post-JSON-and-decode-result helper.
func (c *telegramClient) call(ctx context.Context, method string, body any, out any) error {
	if c.token == "" {
		return fmt.Errorf("telegram: empty token")
	}
	endpoint := "https://api.telegram.org/bot" + c.token + "/" + method
	var bodyReader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var env apiResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("telegram: parse %s: %w", method, err)
	}
	if !env.OK {
		return fmt.Errorf("telegram: %s: %s", method, env.Description)
	}
	if out != nil && env.Result != nil {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("telegram: decode %s result: %w", method, err)
		}
	}
	return nil
}

// GetMe verifies the token and returns the bot's own user object.
// Used at startup to populate state.BotUsername.
func (c *telegramClient) GetMe(ctx context.Context) (*User, error) {
	var u User
	if err := c.call(ctx, "getMe", nil, &u); err != nil {
		return nil, err
	}
	return &u, nil
}

// GetUpdates is the long-polling call. offset is the highest
// update_id we've already processed + 1. timeout (in seconds) is
// how long the server holds the request open when there's nothing
// new — 25s is the canonical pick (under most reverse-proxy 30s
// idle limits).
func (c *telegramClient) GetUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	body := map[string]any{
		"timeout":         timeout,
		"allowed_updates": []string{"message", "callback_query"},
	}
	if offset > 0 {
		body["offset"] = offset
	}
	// Use a longer per-request timeout than the regular client so
	// the long poll has headroom past the server-side hold.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://api.telegram.org/bot"+c.token+"/getUpdates",
		bytes.NewReader(mustJSON(body)))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	pollClient := &http.Client{Timeout: time.Duration(timeout+10) * time.Second}
	resp, err := pollClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	var env struct {
		OK          bool     `json:"ok"`
		Description string   `json:"description"`
		Result      []Update `json:"result"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	if !env.OK {
		return nil, fmt.Errorf("getUpdates: %s", env.Description)
	}
	return env.Result, nil
}

// SendMessage posts a message into a chat.
func (c *telegramClient) SendMessage(ctx context.Context, req SendMessageReq) error {
	return c.call(ctx, "sendMessage", req, nil)
}

// EditMessageText updates an existing message's text and / or
// inline-keyboard markup. The Bot API call name is the same for
// both modifications.
func (c *telegramClient) EditMessageText(ctx context.Context, req EditMessageReq) error {
	return c.call(ctx, "editMessageText", req, nil)
}

// AnswerCallbackQuery acknowledges a callback so the spinner on
// the user's button stops.
func (c *telegramClient) AnswerCallbackQuery(ctx context.Context, req AnswerCallbackQueryReq) error {
	return c.call(ctx, "answerCallbackQuery", req, nil)
}

// mustJSON is a tiny helper that panics on encoding errors —
// callers feed it map[string]any with primitive values, so a
// failure here would be a programmer bug, not a runtime input.
func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

// formURLValues exists for any future migration to multipart/form
// uploads (e.g. sendPhoto for QR codes). Kept as a small private
// helper so the rest of the file doesn't need to know about
// url.Values.
func formURLValues(m map[string]string) url.Values {
	v := url.Values{}
	for k, val := range m {
		v.Set(k, val)
	}
	return v
}

// strconvI64 is a one-line wrapper used by handlers that build
// callback data from int64 IDs.
func strconvI64(i int64) string { return strconv.FormatInt(i, 10) }
