package backend

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/liliang-cn/agent-go/v2/pkg/domain"
	"github.com/liliang-cn/agent-go/v2/pkg/store"
)

// ChatSessionInfo is one past conversation, as listed in the history panel.
type ChatSessionInfo struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Turns     int    `json:"turns"`
	UpdatedAt string `json:"updated_at"`
}

// ChatTurn is a single visible message of a past conversation.
type ChatTurn struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Emotion string `json:"emotion,omitempty"`
}

// maxTitle caps how much of the opening message becomes a session title.
const maxTitle = 60

// Sessions lists past conversations, most recently updated first. Sessions
// whose visible turns are all injected context (a run that never produced a
// real exchange) are skipped — they would list as blank rows.
func (s *Service) Sessions(limit int) []ChatSessionInfo {
	if s == nil || s.svc == nil {
		return []ChatSessionInfo{}
	}
	if limit <= 0 {
		limit = 100
	}
	sessions, err := s.svc.ListSessions(limit)
	if err != nil {
		return []ChatSessionInfo{}
	}

	out := make([]ChatSessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		if sess == nil {
			continue
		}
		turns := visibleTurns(sess.Messages)
		if len(turns) == 0 {
			continue
		}
		out = append(out, ChatSessionInfo{
			ID:        sess.ID,
			Title:     sessionTitle(turns),
			Turns:     len(turns),
			UpdatedAt: sess.UpdatedAt.Format(time.RFC3339),
		})
	}

	// ListSessions already orders by updated_at, but the filtering above can
	// only remove entries — re-sort so callers never depend on that detail.
	sort.SliceStable(out, func(i, j int) bool { return out[i].UpdatedAt > out[j].UpdatedAt })
	return out
}

// SessionTurns returns a past conversation's visible messages, ready to drop
// back into the transcript.
func (s *Service) SessionTurns(id string) []ChatTurn {
	if s == nil || s.svc == nil || strings.TrimSpace(id) == "" {
		return []ChatTurn{}
	}
	sess, err := s.svc.GetSession(id)
	if err != nil || sess == nil {
		return []ChatTurn{}
	}
	return visibleTurns(sess.Messages)
}

// DeleteSession removes a past conversation for good.
//
// agent.Service exposes ListSessions and GetSession but not a delete, and its
// store field is unexported — so this opens the same database directly. The
// delete cascades to chat_messages. A short-lived connection keeps this away
// from the handle the running agent holds; sqlite is in WAL mode, so the two
// coexist, and deleting a conversation is rare enough not to contend.
func (s *Service) DeleteSession(id string) error {
	if s == nil || strings.TrimSpace(id) == "" {
		return errors.New("no session id")
	}
	// The files stay on disk (they may be useful), but stop claiming they belong
	// to a conversation that no longer exists.
	if s.files != nil {
		s.files.forget(id)
	}
	path := filepath.Join(s.dataDir, "agentgo.db")
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("agent database: %w", err)
	}
	db, err := store.NewAgentGoDB(path)
	if err != nil {
		return fmt.Errorf("open agent database: %w", err)
	}
	defer db.Close()
	return db.DeleteSession(id)
}

// visibleTurns strips everything the user never typed or saw: tool traffic, and
// the context the agent injects as extra user messages (date reminders, skill
// discovery, memory recall). Those are real messages to the model but noise in
// a transcript.
func visibleTurns(messages []domain.Message) []ChatTurn {
	turns := make([]ChatTurn, 0, len(messages))
	for i := range messages {
		role := messages[i].Role
		if role != "user" && role != "assistant" {
			continue
		}
		// An assistant turn that only carried tool calls has no text to show.
		if len(messages[i].ToolCalls) > 0 && strings.TrimSpace(messages[i].Content) == "" {
			continue
		}
		content := strings.TrimSpace(messages[i].Content)
		if content == "" || isInjectedContext(content) {
			continue
		}
		turn := ChatTurn{Role: role, Content: content}
		if role == "assistant" {
			turn.Content, turn.Emotion = SplitEmotion(content)
			if strings.TrimSpace(turn.Content) == "" {
				continue
			}
		}
		turns = append(turns, turn)
	}
	return turns
}

// isInjectedContext reports whether a "user" message was actually added by the
// agent rather than typed by the person.
func isInjectedContext(content string) bool {
	return strings.HasPrefix(content, "<system-reminder>") ||
		strings.HasPrefix(content, "<skill-discovery>") ||
		strings.HasPrefix(content, "<memory-recall>")
}

// sessionTitle names a conversation after its opening question.
func sessionTitle(turns []ChatTurn) string {
	for _, t := range turns {
		if t.Role != "user" {
			continue
		}
		// Attachment preambles bury the actual question on a later line.
		title := t.Content
		if strings.HasPrefix(title, "[") {
			if lines := strings.Split(title, "\n"); len(lines) > 0 {
				title = strings.TrimSpace(lines[len(lines)-1])
			}
		}
		title = strings.Join(strings.Fields(title), " ")
		if title == "" {
			continue
		}
		if len([]rune(title)) > maxTitle {
			title = string([]rune(title)[:maxTitle]) + "…"
		}
		return title
	}
	return "Untitled"
}
