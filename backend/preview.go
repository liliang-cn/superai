package backend

import (
	"context"
	"fmt"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/agent"
)

// What the model is about to be told.
//
// A chat turn's first request is assembled from a dozen places — the persona
// and its sections, recalled memory, the plan a previous run left behind,
// extension-contributed context, a skill reminder, the filtered history, and
// whatever tools survived the policies. Every one of them is somewhere a turn
// can go wrong before the model has read a token, and the only way to look at
// the result used to be to send it: a model call, a session write, and
// possibly a mail or a reminder on the way out.
//
// agent-go v3.16.0's Preview is that assembly with the model call and the
// persistence taken out. Nothing here starts a run.

// PreviewMessage is one message the turn would carry.
type PreviewMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// PromptPreview is the first model turn of a chat send, unsent.
type PromptPreview struct {
	SessionID string `json:"sessionId"`
	TaskID    string `json:"taskId"`
	Model     string `json:"model"`

	// SystemPrompt is the first system message — the persona and its
	// sections, which is the part people actually read.
	SystemPrompt string `json:"systemPrompt"`
	// Messages is every message the provider would be handed, system first.
	Messages []PreviewMessage `json:"messages"`
	// Tools is the tool catalogue the turn would offer, in request order.
	Tools []string `json:"tools"`
	// EstimatedTokens is the runtime's own estimate of the messages. Tool
	// schemas are not in it, which matters here: this agent puts its whole
	// catalogue in the schema.
	EstimatedTokens int `json:"estimatedTokens"`

	// ConstraintsDeclared is true when the run would enforce constraints that
	// were declared outright rather than extracted.
	ConstraintsDeclared bool `json:"constraintsDeclared"`
	// ConstraintExtractionSkipped is true when the real run would resolve its
	// constraints by asking the model and the preview did not — the one thing
	// a preview cannot show without doing what it promised not to do.
	ConstraintExtractionSkipped bool `json:"constraintExtractionSkipped"`
	// ForbidTools mirrors the resolved constraint of the same name.
	ForbidTools bool `json:"forbidTools"`
	// Deliverables are the side effects the run would be held to, rendered as
	// "kind: description".
	Deliverables []string `json:"deliverables"`

	// Error is set when the assembly itself failed. A preview is a read, so
	// the caller gets a reason rather than an exception.
	Error string `json:"error,omitempty"`
}

// PreviewPrompt assembles the turn that SendChat would send, and sends
// nothing.
//
// The options are chatRunOptions — the same list Stream builds — minus the
// attachments: a preview is of the text in the composer, and resolving image
// paths that have not been attached yet would describe a different turn.
func (s *Service) PreviewPrompt(ctx context.Context, sessionID, goal string) PromptPreview {
	out := PromptPreview{
		SessionID:    sessionID,
		Messages:     []PreviewMessage{},
		Tools:        []string{},
		Deliverables: []string{},
	}
	if s == nil || s.svc == nil {
		out.Error = "superai: agent service unavailable"
		return out
	}

	p, err := s.svc.Preview(ctx, goal, s.chatRunOptions(sessionID, nil)...)
	if err != nil {
		out.Error = err.Error()
		return out
	}

	out.SessionID = p.SessionID
	out.TaskID = p.TaskID
	out.Model = p.Model
	out.SystemPrompt = p.SystemPrompt
	out.EstimatedTokens = p.EstimatedTokens
	for _, m := range p.Messages {
		out.Messages = append(out.Messages, PreviewMessage{Role: m.Role, Content: m.Content})
	}
	for _, t := range p.Tools {
		out.Tools = append(out.Tools, t.Function.Name)
	}

	out.ConstraintExtractionSkipped = p.ConstraintExtractionSkipped
	out.ConstraintsDeclared = !p.ConstraintExtractionSkipped && !p.Constraints.Empty()
	out.ForbidTools = p.Constraints.ForbidTools
	for _, d := range p.Constraints.Deliverables {
		out.Deliverables = append(out.Deliverables, previewDeliverable(d))
	}
	return out
}

// previewDeliverable renders one required delivery in a line.
func previewDeliverable(d agent.DeliverableRequirement) string {
	kind := strings.TrimSpace(d.Kind)
	if kind == "" {
		kind = "other"
	}
	desc := strings.TrimSpace(d.Description)
	if d.Path != "" {
		desc = strings.TrimSpace(desc + " → " + d.Path)
	}
	if desc == "" {
		return kind
	}
	return fmt.Sprintf("%s: %s", kind, desc)
}
