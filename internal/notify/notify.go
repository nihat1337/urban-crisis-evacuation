// Package notify sends outbound messages (SMS) to citizens. The Sender
// interface hides the gateway: today a simulated sender that logs, tomorrow
// Twilio / an operator SMPP gateway — one file, one line in main.go.
package notify

import (
	"context"
	"log/slog"
	"regexp"
	"strings"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

type Sender interface {
	Name() string
	// Send delivers msg to the E.164-ish phone number `to` and returns the
	// provider's message id (if any).
	Send(ctx context.Context, to, msg string) (providerID string, err error)
	// Simulated reports whether messages are only logged, not delivered.
	Simulated() bool
}

// LogSender is the built-in simulated gateway: every "SMS" is written to the
// structured log and recorded in the notifications journal.
type LogSender struct {
	Log *slog.Logger
}

func NewLogSender(log *slog.Logger) *LogSender { return &LogSender{Log: log} }

func (s *LogSender) Name() string    { return "simulated" }
func (s *LogSender) Simulated() bool { return true }

func (s *LogSender) Send(_ context.Context, to, msg string) (string, error) {
	id := "sim-" + domain.NewID()
	s.Log.Info("SMS (simulated)", "to", to, "id", id, "len", len(msg), "text", msg)
	return id, nil
}

var phoneRe = regexp.MustCompile(`^\+?[0-9]{9,15}$`)

// NormalizePhone strips spaces/dashes/parentheses and validates the result.
// Azerbaijani local numbers (0XX…) are promoted to +994.
func NormalizePhone(raw string) (string, bool) {
	p := strings.NewReplacer(" ", "", "-", "", "(", "", ")", "", ".", "").Replace(strings.TrimSpace(raw))
	if strings.HasPrefix(p, "00") {
		p = "+" + p[2:]
	}
	if strings.HasPrefix(p, "0") && len(p) == 10 { // 0XX XXX XX XX
		p = "+994" + p[1:]
	}
	if !phoneRe.MatchString(p) {
		return "", false
	}
	if !strings.HasPrefix(p, "+") {
		p = "+" + p
	}
	return p, true
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

func ValidEmail(e string) bool { return e == "" || emailRe.MatchString(strings.TrimSpace(e)) }
