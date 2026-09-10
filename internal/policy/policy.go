package policy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

type filePolicy struct {
	Messages struct {
		OwnerAliases          []string `json:"owner_aliases"`
		AllowedDirect         []string `json:"allowed_direct"`
		ExcludedConversations []string `json:"excluded_conversations,omitempty"`
	} `json:"messages"`
}

type Policy struct {
	owners   map[string]struct{}
	direct   map[string]struct{}
	excluded map[string]struct{}
}

func Load(path string) (*Policy, error) {
	data, err := securefile.ReadOwnerFile(path, 1<<20)
	if err != nil {
		return nil, fmt.Errorf("read policy: %w", err)
	}
	var raw filePolicy
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse policy: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("parse policy: expected one object")
	}
	p := &Policy{owners: map[string]struct{}{}, direct: map[string]struct{}{}, excluded: map[string]struct{}{}}
	for _, value := range raw.Messages.OwnerAliases {
		normalized, err := NormalizeIdentity(value)
		if err != nil {
			return nil, fmt.Errorf("policy owner_aliases: %w", err)
		}
		p.owners[normalized] = struct{}{}
	}
	if len(p.owners) == 0 {
		return nil, fmt.Errorf("policy: messages.owner_aliases must not be empty")
	}
	for _, value := range raw.Messages.AllowedDirect {
		normalized, err := NormalizeIdentity(value)
		if err != nil {
			return nil, fmt.Errorf("policy allowed_direct: %w", err)
		}
		if _, own := p.owners[normalized]; own {
			return nil, fmt.Errorf("policy: an owner alias cannot authorize a direct conversation")
		}
		p.direct[normalized] = struct{}{}
	}
	for _, guid := range raw.Messages.ExcludedConversations {
		guid = strings.TrimSpace(guid)
		if guid == "" || len(guid) > 512 {
			return nil, fmt.Errorf("policy: excluded conversation GUID is empty or too long")
		}
		p.excluded[guid] = struct{}{}
	}
	return p, nil
}

type Conversation struct {
	GUID         string
	Identifier   string
	IsGroup      *bool
	Participants *[]string
}

type Decision struct {
	Valid        bool
	Allowed      bool
	IsGroup      bool
	Participants []string
}

func (p *Policy) Authorize(c Conversation) Decision {
	if p == nil || c.IsGroup == nil || c.Participants == nil || strings.TrimSpace(c.GUID) == "" {
		return Decision{}
	}
	parts := strings.Split(c.GUID, ";")
	if len(parts) != 3 || parts[0] == "" || parts[2] == "" || (parts[1] != "+" && parts[1] != "-") || c.Identifier == "" {
		return Decision{}
	}
	marker := parts[1] == "+"
	if *c.IsGroup != marker {
		return Decision{}
	}
	// A structured identifier must agree with the GUID. The plain identifier
	// used by native Messages rows must identify the same conversation.
	identifier := c.Identifier
	if strings.Contains(identifier, ";") {
		if identifier != c.GUID {
			return Decision{}
		}
		identifier = parts[2]
	}
	if marker && identifier != parts[2] {
		return Decision{}
	}
	participants := make([]string, 0, len(*c.Participants))
	seen := map[string]struct{}{}
	for _, raw := range *c.Participants {
		normalized, err := NormalizeIdentity(raw)
		if err != nil {
			return Decision{}
		}
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		participants = append(participants, normalized)
	}
	if len(participants) == 0 {
		return Decision{}
	}
	if marker {
		_, denied := p.excluded[c.GUID]
		return Decision{Valid: true, Allowed: !denied, IsGroup: true, Participants: participants}
	}
	if len(participants) != 1 {
		return Decision{}
	}
	identifier, err := NormalizeIdentity(identifier)
	if err != nil || identifier != participants[0] {
		return Decision{}
	}
	guidIdentity, err := NormalizeIdentity(parts[2])
	if err != nil || guidIdentity != identifier {
		return Decision{}
	}
	if _, own := p.owners[identifier]; own {
		return Decision{Valid: true, Participants: participants}
	}
	_, allowed := p.direct[identifier]
	if _, denied := p.excluded[c.GUID]; denied {
		allowed = false
	}
	return Decision{Valid: true, Allowed: allowed, Participants: participants}
}
