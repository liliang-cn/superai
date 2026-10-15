package backend

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// The queen's name, as the person knows her.
//
// Everything internal stays "queen" — the hive role, the agent name, the RPCs,
// the pods. What changes is what a client shows the person and what the model
// is told it is called. QueenName is that name and QueenAliases the other
// names it answers to: in an @ at the front of a message, and, through the
// persona, anywhere a person writes one of them.
//
// Matching is by name and nothing else: a name is either one of these, compared
// without regard to case, or it is not.

// DefaultQueenName is the name nobody chose. With it and no aliases nothing
// differs from before the setting existed.
const DefaultQueenName = "queen"

// maxQueenAliases bounds the list so the persona line stays a line.
const maxQueenAliases = 8

// queenNamePattern is what can follow an @ (addressedTo in app_remote.go reads
// letters, digits, '-', '_' and '.'): a name with a space in it could be shown
// but never addressed, which is a name that half works.
var queenNamePattern = regexp.MustCompile(`^[\p{L}\p{N}._-]{1,32}$`)

// QueenDisplayName is the name the clients show for the queen.
func (s *Settings) QueenDisplayName() string {
	if s == nil {
		return DefaultQueenName
	}
	if n := strings.TrimSpace(s.QueenName); n != "" {
		return n
	}
	return DefaultQueenName
}

// QueenAliasList is the configured aliases, cleaned: no blanks, no repeats,
// none equal to the name itself.
func (s *Settings) QueenAliasList() []string {
	if s == nil {
		return []string{}
	}
	return cleanQueenAliases(s.QueenDisplayName(), s.QueenAliases)
}

// QueenNames is every name the queen answers to: "queen" always, then the
// chosen name and the aliases.
func (s *Settings) QueenNames() []string {
	out := []string{DefaultQueenName}
	add := func(n string) {
		for _, e := range out {
			if strings.EqualFold(e, n) {
				return
			}
		}
		out = append(out, n)
	}
	add(s.QueenDisplayName())
	for _, a := range s.QueenAliasList() {
		add(a)
	}
	return out
}

// IsQueenName reports whether name is one of QueenNames, ignoring case.
func (s *Settings) IsQueenName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, n := range s.QueenNames() {
		if strings.EqualFold(n, name) {
			return true
		}
	}
	return false
}

// QueenNamed reports whether the person has named the queen or given her
// aliases — whether anything about her name differs from the default.
func (s *Settings) QueenNamed() bool {
	return s.QueenDisplayName() != DefaultQueenName || len(s.QueenAliasList()) > 0
}

// ValidateQueenName checks a name and its aliases before they are saved.
func ValidateQueenName(name string, aliases []string) error {
	name = strings.TrimSpace(name)
	if name != "" && !queenNamePattern.MatchString(name) {
		return fmt.Errorf("name %q: use 1-32 letters, digits, '.', '_' or '-' (no spaces), so it can follow an @", name)
	}
	n := 0
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if !queenNamePattern.MatchString(a) {
			return fmt.Errorf("alias %q: use 1-32 letters, digits, '.', '_' or '-' (no spaces), so it can follow an @", a)
		}
		n++
	}
	if n > maxQueenAliases {
		return fmt.Errorf("at most %d aliases", maxQueenAliases)
	}
	return nil
}

func cleanQueenAliases(name string, aliases []string) []string {
	out := []string{}
	for _, a := range aliases {
		a = strings.TrimSpace(a)
		if a == "" || strings.EqualFold(a, name) || strings.EqualFold(a, DefaultQueenName) {
			continue
		}
		dup := false
		for _, e := range out {
			if strings.EqualFold(e, a) {
				dup = true
				break
			}
		}
		if !dup {
			out = append(out, a)
		}
	}
	return out
}

// normalizeQueen trims the name and aliases and drops what adds nothing.
func (s *Settings) normalizeQueen() {
	s.QueenName = s.QueenDisplayName()
	s.QueenAliases = cleanQueenAliases(s.QueenName, s.QueenAliases)
	if len(s.QueenAliases) == 0 {
		s.QueenAliases = nil
	}
}

// queenIdentity is queen.json: the name and aliases set from a client.
//
// A file of its own for the reason linked.json is one: in the hive's
// containers settings.json is rewritten from a Secret on every start, and a
// name given on the phone would be "queen" again after the next restart.
type queenIdentity struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
}

func queenPath() string { return filepath.Join(DataDir(), "queen.json") }

// mergeQueenIdentity lays queen.json over what settings.json said. The file
// exists only once a name was set from a client, and that is the later word.
func (s *Settings) mergeQueenIdentity() {
	raw, err := os.ReadFile(queenPath())
	if err != nil {
		return
	}
	var q queenIdentity
	if json.Unmarshal(raw, &q) != nil {
		return
	}
	s.QueenName, s.QueenAliases = q.Name, q.Aliases
	s.normalizeQueen()
}

// saveQueenIdentity writes queen.json when the queen has a name of her own and
// removes it when she is back to the default, so a default chosen here does not
// shadow a name an operator later puts in the Secret.
func (s *Settings) saveQueenIdentity() error {
	if !s.QueenNamed() {
		if err := os.Remove(queenPath()); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	raw, err := json.MarshalIndent(queenIdentity{Name: s.QueenDisplayName(), Aliases: s.QueenAliasList()}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(queenPath(), raw, 0o644)
}

// queenNameSection tells the model the name people call it by. Empty while the
// queen is unnamed, so the default prompt is the prompt it always was.
func queenNameSection(s *Settings) string {
	if s == nil || s.Hive.Role == HiveRoleWorker || !s.QueenNamed() {
		return ""
	}
	name := s.QueenDisplayName()
	others := []string{}
	for _, n := range s.QueenNames() {
		if n != name {
			others = append(others, n)
		}
	}
	return fmt.Sprintf("\n\nThe person calls you %s; %s also mean you, with or without an @ in front. When they use one of these names they are talking to you, not asking you to pass the message on. Call yourself %s.",
		name, strings.Join(others, ", "), name)
}
