package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Bot is a minimal Telegram Bot API client for the interactive approval flow.
type Bot struct {
	token  string
	chatID string
	http   *http.Client
}

// NewBot builds a Telegram bot client.
func NewBot(token, chatID string) *Bot {
	return &Bot{
		token:  token,
		chatID: chatID,
		http:   &http.Client{Timeout: 65 * time.Second}, // > long-poll timeout
	}
}

// Button is a single inline keyboard button carrying callback data.
type Button struct {
	Text string
	Data string
}

type apiResp struct {
	OK          bool            `json:"ok"`
	Description string          `json:"description"`
	Result      json.RawMessage `json:"result"`
}

func (b *Bot) call(ctx context.Context, method string, form url.Values) (json.RawMessage, error) {
	endpoint := fmt.Sprintf("https://api.telegram.org/bot%s/%s", b.token, method)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := b.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	var r apiResp
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("telegram %s: %s", method, strings.TrimSpace(string(body)))
	}
	if !r.OK {
		return nil, fmt.Errorf("telegram %s: %s", method, r.Description)
	}
	return r.Result, nil
}

// SendHTML sends a message (parse_mode=HTML) and returns its message_id.
func (b *Bot) SendHTML(ctx context.Context, text string) (int64, error) {
	form := url.Values{
		"chat_id":    {b.chatID},
		"text":       {text},
		"parse_mode": {"HTML"},
	}
	raw, err := b.call(ctx, "sendMessage", form)
	if err != nil {
		return 0, err
	}
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &msg)
	return msg.MessageID, nil
}

// SendButtons sends an HTML message with an inline keyboard and returns its id.
func (b *Bot) SendButtons(ctx context.Context, text string, rows [][]Button) (int64, error) {
	kb := map[string]any{"inline_keyboard": toKeyboard(rows)}
	kbJSON, _ := json.Marshal(kb)
	form := url.Values{
		"chat_id":      {b.chatID},
		"text":         {text},
		"parse_mode":   {"HTML"},
		"reply_markup": {string(kbJSON)},
	}
	raw, err := b.call(ctx, "sendMessage", form)
	if err != nil {
		return 0, err
	}
	var msg struct {
		MessageID int64 `json:"message_id"`
	}
	_ = json.Unmarshal(raw, &msg)
	return msg.MessageID, nil
}

// EditKeyboard replaces the inline keyboard of an existing message. This is
// what makes toggle buttons work: the message stays, only the markup changes.
func (b *Bot) EditKeyboard(ctx context.Context, messageID int64, rows [][]Button) error {
	kbJSON, _ := json.Marshal(map[string]any{"inline_keyboard": toKeyboard(rows)})
	_, err := b.call(ctx, "editMessageReplyMarkup", url.Values{
		"chat_id":      {b.chatID},
		"message_id":   {strconv.FormatInt(messageID, 10)},
		"reply_markup": {string(kbJSON)},
	})
	return err
}

// EditMessage replaces text and keyboard of an existing message. Passing no
// rows removes the buttons, so a decided dialog cannot be pressed again.
func (b *Bot) EditMessage(ctx context.Context, messageID int64, text string, rows [][]Button) error {
	kbJSON, _ := json.Marshal(map[string]any{"inline_keyboard": toKeyboard(rows)})
	_, err := b.call(ctx, "editMessageText", url.Values{
		"chat_id":      {b.chatID},
		"message_id":   {strconv.FormatInt(messageID, 10)},
		"text":         {text},
		"parse_mode":   {"HTML"},
		"reply_markup": {string(kbJSON)},
	})
	return err
}

func toKeyboard(rows [][]Button) [][]map[string]string {
	out := make([][]map[string]string, 0, len(rows))
	for _, row := range rows {
		r := make([]map[string]string, 0, len(row))
		for _, btn := range row {
			r = append(r, map[string]string{"text": btn.Text, "callback_data": btn.Data})
		}
		out = append(out, r)
	}
	return out
}

type update struct {
	UpdateID      int64 `json:"update_id"`
	CallbackQuery *struct {
		ID   string `json:"id"`
		Data string `json:"data"`
		Msg  struct {
			MessageID int64 `json:"message_id"`
			Chat      struct {
				ID int64 `json:"id"`
			} `json:"chat"`
		} `json:"message"`
	} `json:"callback_query"`
}

// ErrTimeout is returned by AwaitCallback when no answer arrives in time.
type ErrTimeout struct{}

func (ErrTimeout) Error() string { return "keine Antwort innerhalb des Zeitlimits" }

// CallbackAction tells AwaitDecision how to react to a button press.
type CallbackAction struct {
	// Toast is the short confirmation shown on the pressed button.
	Toast string
	// Keyboard, when non-nil, replaces the message's inline keyboard. This is
	// how a toggled checkbox becomes visible.
	Keyboard [][]Button
	// Done ends the dialog.
	Done bool
}

// AwaitDecision long-polls for callback queries belonging to messageID and
// feeds each one to press, until press reports Done or timeout elapses.
// Button presses are acknowledged and keyboard updates applied, so several
// buttons can be toggled before the dialog is finished. Returns ErrTimeout on
// deadline.
//
// Note: getUpdates requires that NO webhook is set for this bot token. Use a
// dedicated bot for dredge if another tool (e.g. n8n) uses webhooks.
func (b *Bot) AwaitDecision(ctx context.Context, messageID int64, timeout time.Duration, press func(data string) CallbackAction) error {
	deadline := time.Now().Add(timeout)
	var offset int64

	// Prime the offset so we skip updates that predate our message.
	if raw, err := b.call(ctx, "getUpdates", url.Values{"timeout": {"0"}, "offset": {"-1"}}); err == nil {
		var ups []update
		if json.Unmarshal(raw, &ups) == nil && len(ups) > 0 {
			offset = ups[len(ups)-1].UpdateID + 1
		}
	}

	for time.Now().Before(deadline) {
		remaining := time.Until(deadline)
		poll := 50
		if remaining < 50*time.Second {
			poll = int(remaining.Seconds())
		}
		if poll < 1 {
			poll = 1
		}
		form := url.Values{
			"timeout":         {fmt.Sprintf("%d", poll)},
			"offset":          {fmt.Sprintf("%d", offset)},
			"allowed_updates": {`["callback_query"]`},
		}
		raw, err := b.call(ctx, "getUpdates", form)
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
			continue
		}
		var ups []update
		if err := json.Unmarshal(raw, &ups); err != nil {
			continue
		}
		for _, u := range ups {
			offset = u.UpdateID + 1
			cq := u.CallbackQuery
			if cq == nil {
				continue
			}
			if messageID != 0 && cq.Msg.MessageID != messageID {
				continue // stale / different message
			}
			act := press(cq.Data)
			_ = b.answer(ctx, cq.ID, act.Toast)
			if act.Keyboard != nil {
				// Rein kosmetisch – ein Fehler hier darf die Freigabe nicht kippen.
				_ = b.EditKeyboard(ctx, messageID, act.Keyboard)
			}
			if act.Done {
				return nil
			}
		}
	}
	return ErrTimeout{}
}

func (b *Bot) answer(ctx context.Context, callbackID, text string) error {
	_, err := b.call(ctx, "answerCallbackQuery", url.Values{
		"callback_query_id": {callbackID},
		"text":              {text},
	})
	return err
}
