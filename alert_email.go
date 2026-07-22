package golens

import (
	"context"
	"fmt"
	"log"
	"strings"
)

// LogNotifier is an EmailNotifier that logs messages to stdout instead of
// sending real email. Useful for development and testing.
//
//	r.SetEmailNotifier(golens.NewLogNotifier())
type LogNotifier struct {
	Logger *log.Logger // nil defaults to log.Default()
}

// NewLogNotifier returns an EmailNotifier that prints each message to stdout.
func NewLogNotifier() *LogNotifier {
	return &LogNotifier{}
}

func (n *LogNotifier) Send(_ context.Context, msg EmailMessage) error {
	l := n.Logger
	if l == nil {
		l = log.Default()
	}

	var b strings.Builder
	b.WriteString("──── Alert Email ────\n")
	b.WriteString(fmt.Sprintf("  To:      %s\n", strings.Join(msg.To, ", ")))
	if len(msg.CC) > 0 {
		b.WriteString(fmt.Sprintf("  CC:      %s\n", strings.Join(msg.CC, ", ")))
	}
	if len(msg.BCC) > 0 {
		b.WriteString(fmt.Sprintf("  BCC:     %s\n", strings.Join(msg.BCC, ", ")))
	}
	if len(msg.ReplyTo) > 0 {
		b.WriteString(fmt.Sprintf("  ReplyTo: %s\n", strings.Join(msg.ReplyTo, ", ")))
	}
	b.WriteString(fmt.Sprintf("  Subject: %s\n", msg.Subject))
	format := "text"
	if msg.IsHTML {
		format = "html"
	}
	b.WriteString(fmt.Sprintf("  Format:  %s\n", format))
	b.WriteString("  Body:\n")
	for _, line := range strings.Split(msg.Body, "\n") {
		fmt.Fprintf(&b, "    %s\n", line)
	}
	b.WriteString("─────────────────────")

	l.Println(b.String())
	return nil
}
