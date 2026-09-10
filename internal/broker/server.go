package broker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/jimmingcheng/safe-imsg/internal/backend"
	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/contacts"
	contentfilter "github.com/jimmingcheng/safe-imsg/internal/filter"
	"github.com/jimmingcheng/safe-imsg/internal/policy"
	"github.com/jimmingcheng/safe-imsg/internal/rpc"
	"github.com/jimmingcheng/safe-imsg/internal/securefile"
)

const (
	maxResponseBytes = 8 << 20
	maxTextBytes     = 64 << 10
)

type Dependencies struct {
	Backend       backend.Service
	LoadPolicy    func(string) (*policy.Policy, error)
	PeerUID       func(*net.UnixConn) (uint32, error)
	ContactsFetch contacts.FetchFunc
}

type Server struct {
	cfg         config.Config
	deps        Dependencies
	connections chan struct{}
	contacts    *contacts.Manager
}

func New(cfg config.Config) (*Server, error) { return NewWithDeps(cfg, Dependencies{}) }

func NewWithDeps(cfg config.Config, deps Dependencies) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if deps.Backend == nil {
		process, err := backend.New(cfg)
		if err != nil {
			return nil, err
		}
		deps.Backend = process
	}
	if deps.LoadPolicy == nil {
		deps.LoadPolicy = policy.Load
	}
	if deps.PeerUID == nil {
		deps.PeerUID = peerUID
	}
	server := &Server{cfg: cfg, deps: deps, connections: make(chan struct{}, 32)}
	if cfg.Contacts != nil {
		fetch := deps.ContactsFetch
		if fetch == nil {
			if err := contacts.CheckHelper(cfg.Contacts.HelperPath); err != nil {
				return nil, err
			}
			fetch = contacts.Fetch
		}
		server.contacts = contacts.NewManager(*cfg.Contacts, fetch, nil)
	}
	return server, nil
}

func (s *Server) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if s.contacts != nil {
		done := make(chan struct{})
		go func() { defer close(done); s.contacts.Run(ctx) }()
		defer func() { cancel(); <-done }()
	}
	dir := filepath.Dir(s.cfg.SocketPath)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := checkSocketDir(dir); err != nil {
		return err
	}
	lock, err := acquireLock(s.cfg.SocketPath + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := prepareSocketPath(s.cfg.SocketPath); err != nil {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: s.cfg.SocketPath, Net: "unix"})
	if err != nil {
		return fmt.Errorf("listen on broker socket: %w", err)
	}
	// Go otherwise unlinks the path on Close before our identity check runs.
	listener.SetUnlinkOnClose(false)
	var handlers sync.WaitGroup
	createdInfo, statErr := os.Lstat(s.cfg.SocketPath)
	if statErr != nil {
		listener.Close()
		return fmt.Errorf("inspect broker socket: %w", statErr)
	}
	defer func() {
		cancel()
		_ = listener.Close()
		handlers.Wait()
		if current, err := os.Lstat(s.cfg.SocketPath); err == nil && os.SameFile(createdInfo, current) {
			_ = os.Remove(s.cfg.SocketPath)
		}
	}()
	mode, _ := s.cfg.SocketFileMode()
	if err := os.Chmod(s.cfg.SocketPath, mode); err != nil {
		return fmt.Errorf("set broker socket mode: %w", err)
	}
	stopListening := context.AfterFunc(ctx, func() {
		_ = listener.Close()
	})
	defer stopListening()
	for {
		conn, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept broker connection: %w", err)
		}
		select {
		case s.connections <- struct{}{}:
			handlers.Add(1)
			go func() {
				defer handlers.Done()
				defer func() { <-s.connections }()
				s.handleConn(ctx, conn)
			}()
		default:
			_ = conn.Close()
		}
	}
}

func checkSocketDir(path string) error {
	if err := securefile.CheckParents(path); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect socket directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("unsafe socket directory: expected a non-symlink directory")
	}
	if info.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("unsafe socket directory: group/other writable")
	}
	uid, ok := fileOwnerUID(info)
	if !ok || uid != uint32(os.Geteuid()) {
		return fmt.Errorf("unsafe socket directory: not owned by broker user")
	}
	return nil
}

func prepareSocketPath(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing socket: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("refusing to replace non-socket or symlink at socket path")
	}
	conn, dialErr := net.DialTimeout("unix", path, 250*time.Millisecond)
	if dialErr == nil {
		_ = conn.Close()
		return fmt.Errorf("broker socket is already active")
	}
	if !errors.Is(dialErr, syscall.ECONNREFUSED) && !errors.Is(dialErr, os.ErrNotExist) {
		return fmt.Errorf("existing socket could be active; refusing to remove it")
	}
	current, err := os.Lstat(path)
	if err != nil || !os.SameFile(info, current) {
		return fmt.Errorf("socket path changed while probing it")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove stale broker socket: %w", err)
	}
	return nil
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	stopClose := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stopClose()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	unixConn, ok := conn.(*net.UnixConn)
	if !ok {
		_ = writeResponse(conn, rpc.Failure("", "internal_error", "invalid transport", false))
		return
	}
	uid, err := s.deps.PeerUID(unixConn)
	if err != nil {
		_ = writeResponse(conn, rpc.Failure("", "peer_auth_failed", "could not authenticate peer", false))
		return
	}
	if uid != s.cfg.ClientUID {
		_ = writeResponse(conn, rpc.Failure("", "unauthorized_peer", "peer uid is not allowed", false))
		return
	}
	payload, err := rpc.ReadFrame(conn, config.RequestMaxBytes())
	if err != nil {
		_ = writeResponse(conn, rpc.Failure("", "invalid_request", "request frame is invalid", false))
		return
	}
	var req rpc.Request
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		_ = writeResponse(conn, rpc.Failure("", "invalid_request", "request must be valid JSON", false))
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		_ = writeResponse(conn, rpc.Failure("", "invalid_request", "request must contain one object", false))
		return
	}
	if err := rpc.ValidateRequest(req); err != nil {
		code := "invalid_request"
		var validation *rpc.ValidationError
		if errors.As(err, &validation) {
			code = validation.Code
		}
		_ = writeResponse(conn, rpc.Failure(req.ID, code, err.Error(), false))
		return
	}
	_ = conn.SetWriteDeadline(time.Now().Add(time.Duration(s.cfg.BackendTimeoutMillis)*time.Millisecond + time.Second))
	_ = writeResponse(conn, s.dispatch(ctx, req))
}

func writeResponse(conn net.Conn, resp rpc.Response) error {
	payload, err := json.Marshal(resp)
	if err != nil {
		return err
	}
	if len(payload) > maxResponseBytes {
		payload, _ = json.Marshal(rpc.Failure(resp.ID, "response_too_large", "response exceeds transport limit", false))
	}
	return rpc.WriteFrame(conn, payload)
}

func (s *Server) dispatch(ctx context.Context, req rpc.Request) (resp rpc.Response) {
	// A single deadline covers watch/history and all subsequent chat lookups.
	ctx, cancel := context.WithTimeout(ctx, time.Duration(s.cfg.BackendTimeoutMillis)*time.Millisecond)
	defer cancel()
	defer func() {
		if ctx.Err() != nil {
			resp = backendFailure(req.ID, ctx.Err())
		}
	}()
	switch req.Method {
	case rpc.MethodSystemPing:
		if err := decodeEmpty(req.Params); err != nil {
			return invalidParams(req.ID, err)
		}
		return rpc.Success(req.ID, map[string]bool{"pong": true})
	case rpc.MethodSystemInfo:
		if err := decodeEmpty(req.Params); err != nil {
			return invalidParams(req.ID, err)
		}
		var contactsInfo *rpc.ContactsPolicyInfo
		if s.contacts != nil {
			value := rpc.ContactsPolicyInfo(s.contacts.Status())
			contactsInfo = &value
		}
		return rpc.Success(req.ID, rpc.SystemInfo{
			Service: "safe-imsgd", ProtocolVersion: rpc.Version1, Instance: s.cfg.Instance,
			AccountID: s.cfg.AccountID, DatabaseGeneration: s.deps.Backend.Generation(), MaxResults: s.cfg.MaxResults,
			Methods:        []string{rpc.MethodSystemPing, rpc.MethodSystemInfo, rpc.MethodListChats, rpc.MethodHistory, rpc.MethodGetMessage, rpc.MethodCollect},
			ContactsPolicy: contactsInfo,
		})
	case rpc.MethodListChats:
		return s.listChats(ctx, req)
	case rpc.MethodHistory:
		return s.history(ctx, req)
	case rpc.MethodGetMessage:
		return s.getMessage(ctx, req)
	case rpc.MethodCollect:
		return s.collect(ctx, req)
	default:
		return rpc.Failure(req.ID, "method_not_allowed", "method is not exposed by this broker", false)
	}
}

func decodeEmpty(raw json.RawMessage) error {
	var params struct{}
	return rpc.DecodeParams(raw, &params)
}

func invalidParams(id string, err error) rpc.Response {
	return rpc.Failure(id, "invalid_params", "invalid parameters: "+err.Error(), false)
}

func normalizeLimit(requested, defaultValue, maximum int) (int, error) {
	if requested == 0 {
		return min(defaultValue, maximum), nil
	}
	if requested < 1 || requested > maximum {
		return 0, fmt.Errorf("limit must be between 1 and %d", maximum)
	}
	return requested, nil
}

func (s *Server) loadPolicy(id string) (*policy.Policy, *rpc.Response) {
	p, err := s.deps.LoadPolicy(s.cfg.PolicyPath)
	if err == nil && s.contacts != nil {
		var identities []string
		identities, err = s.contacts.Get()
		if err == nil {
			p, err = p.WithDirect(identities)
		}
	}
	if err != nil {
		resp := rpc.Failure(id, "policy_unavailable", "policy could not be loaded safely", false)
		return nil, &resp
	}
	return p, nil
}

func (s *Server) verifyGeneration(id, got string) *rpc.Response {
	if got == "" {
		resp := rpc.Failure(id, "invalid_params", "database_generation is required", false)
		return &resp
	}
	if got != s.deps.Backend.Generation() {
		resp := rpc.Failure(id, "stale_generation", "database generation does not match this broker", false)
		return &resp
	}
	return nil
}

func toConversation(chat backend.RawChat) policy.Conversation {
	return policy.Conversation{GUID: chat.GUID, Identifier: chat.Identifier, IsGroup: chat.IsGroup, Participants: chat.Participants}
}

func validChat(chat backend.RawChat) bool {
	return chat.ID > 0 && len(chat.GUID) > 0 && len(chat.GUID) <= 512 && len(chat.Identifier) <= 512 &&
		len(chat.Service) > 0 && len(chat.Service) <= 64
}

func (s *Server) matchesAccount(chat backend.RawChat) bool {
	return chat.AccountID != nil && *chat.AccountID == s.cfg.BackendAccountID
}

func (s *Server) exposeChat(chat backend.RawChat, decision policy.Decision) rpc.Chat {
	return rpc.Chat{AccountID: s.cfg.AccountID, DatabaseGeneration: s.deps.Backend.Generation(), ChatID: chat.ID,
		ChatGUID: chat.GUID, Service: chat.Service, IsGroup: decision.IsGroup, Participants: decision.Participants}
}

func (s *Server) listChats(ctx context.Context, req rpc.Request) rpc.Response {
	var params rpc.ListChatsParams
	if err := rpc.DecodeParams(req.Params, &params); err != nil {
		return invalidParams(req.ID, err)
	}
	limit, err := normalizeLimit(params.Limit, 20, s.cfg.MaxResults)
	if err != nil {
		return invalidParams(req.ID, err)
	}
	rows, err := s.deps.Backend.ListChats(ctx, s.cfg.MaxChatScan)
	if err != nil {
		return backendFailure(req.ID, err)
	}
	p, failure := s.loadPolicy(req.ID)
	if failure != nil {
		return *failure
	}
	result := make([]rpc.Chat, 0, limit)
	complete := len(rows) < s.cfg.MaxChatScan
	for _, row := range rows {
		if !validChat(row) {
			return rpc.Failure(req.ID, "backend_invalid", "backend returned invalid conversation metadata", false)
		}
		if !s.matchesAccount(row) {
			continue
		}
		decision := p.Authorize(toConversation(row))
		if decision.Allowed {
			if len(result) == limit {
				complete = false
			} else {
				result = append(result, s.exposeChat(row, decision))
			}
		}
	}
	return rpc.Success(req.ID, rpc.ListChatsResult{Chats: result, ScanComplete: complete})
}

func (s *Server) authorizedChat(ctx context.Context, id string, chatID int64, p *policy.Policy) (backend.RawChat, policy.Decision, *rpc.Response) {
	if chatID <= 0 {
		resp := rpc.Failure(id, "invalid_params", "chat_id must be positive", false)
		return backend.RawChat{}, policy.Decision{}, &resp
	}
	chat, err := s.deps.Backend.Chat(ctx, chatID)
	if err != nil {
		resp := backendFailure(id, err)
		return backend.RawChat{}, policy.Decision{}, &resp
	}
	if !validChat(chat) || chat.ID != chatID {
		resp := rpc.Failure(id, "backend_invalid", "backend returned invalid conversation metadata", false)
		return backend.RawChat{}, policy.Decision{}, &resp
	}
	if !s.matchesAccount(chat) {
		resp := rpc.Failure(id, "not_visible", "conversation is not visible", false)
		return backend.RawChat{}, policy.Decision{}, &resp
	}
	decision := p.Authorize(toConversation(chat))
	if !decision.Allowed {
		resp := rpc.Failure(id, "not_visible", "conversation is not visible", false)
		return backend.RawChat{}, policy.Decision{}, &resp
	}
	return chat, decision, nil
}

func sameConversation(message backend.RawMessage, chat backend.RawChat, p *policy.Policy) (policy.Decision, bool) {
	if message.ChatID != chat.ID || message.ChatGUID != chat.GUID || message.ChatIdentifier != chat.Identifier || message.IsGroup == nil || chat.IsGroup == nil || *message.IsGroup != *chat.IsGroup {
		return policy.Decision{}, false
	}
	messageDecision := p.Authorize(toConversation(message.Conversation()))
	chatDecision := p.Authorize(toConversation(chat))
	if !messageDecision.Valid || !chatDecision.Valid || messageDecision.IsGroup != chatDecision.IsGroup || messageDecision.Allowed != chatDecision.Allowed {
		return policy.Decision{}, false
	}
	a := append([]string(nil), messageDecision.Participants...)
	b := append([]string(nil), chatDecision.Participants...)
	sort.Strings(a)
	sort.Strings(b)
	if strings.Join(a, "\x00") != strings.Join(b, "\x00") {
		return policy.Decision{}, false
	}
	return messageDecision, true
}

func (s *Server) exposeMessage(raw backend.RawMessage, chat backend.RawChat, p *policy.Policy) (rpc.Message, bool, error) {
	if raw.ID <= 0 || raw.ChatID <= 0 || raw.GUID == "" || len(raw.GUID) > 512 || raw.IsFromMe == nil ||
		!utf8.ValidString(raw.Text) || len(raw.Text) > maxTextBytes {
		return rpc.Message{}, false, fmt.Errorf("invalid message fields")
	}
	if !s.matchesAccount(chat) {
		return rpc.Message{}, false, nil
	}
	decision, ok := sameConversation(raw, chat, p)
	if !ok {
		return rpc.Message{}, false, fmt.Errorf("contradictory message conversation metadata")
	}
	if !decision.Allowed {
		return rpc.Message{}, false, nil
	}
	if _, err := time.Parse(time.RFC3339Nano, raw.CreatedAt); err != nil {
		return rpc.Message{}, false, fmt.Errorf("invalid message timestamp")
	}
	if contentfilter.Suppress(raw.Text) {
		return rpc.Message{}, false, nil
	}
	sender := ""
	if !*raw.IsFromMe {
		normalized, err := policy.NormalizeIdentity(raw.Sender)
		if err != nil {
			return rpc.Message{}, false, fmt.Errorf("invalid message sender")
		}
		found := false
		for _, participant := range decision.Participants {
			if participant == normalized {
				found = true
				break
			}
		}
		if !found {
			return rpc.Message{}, false, fmt.Errorf("sender is not a conversation participant")
		}
		sender = normalized
	}
	return rpc.Message{AccountID: s.cfg.AccountID, DatabaseGeneration: s.deps.Backend.Generation(), RowID: raw.ID,
		ChatID: raw.ChatID, ChatGUID: raw.ChatGUID, GUID: raw.GUID, Sender: sender, FromMe: *raw.IsFromMe,
		Text: raw.Text, CreatedAt: raw.CreatedAt}, true, nil
}

func (s *Server) history(ctx context.Context, req rpc.Request) rpc.Response {
	var params rpc.GenerationParams
	if err := rpc.DecodeParams(req.Params, &params); err != nil {
		return invalidParams(req.ID, err)
	}
	if failure := s.verifyGeneration(req.ID, params.DatabaseGeneration); failure != nil {
		return *failure
	}
	limit, err := normalizeLimit(params.Limit, 50, s.cfg.MaxResults)
	if err != nil {
		return invalidParams(req.ID, err)
	}
	p, failure := s.loadPolicy(req.ID)
	if failure != nil {
		return *failure
	}
	chat, _, failure := s.authorizedChat(ctx, req.ID, params.ChatID, p)
	if failure != nil {
		return *failure
	}
	rows, err := s.deps.Backend.History(ctx, params.ChatID, s.cfg.MaxMessageScan)
	if err != nil {
		return backendFailure(req.ID, err)
	}
	// Reload immediately before serialization so revocation cannot be defeated
	// by policy changes while the backend was running.
	p, failure = s.loadPolicy(req.ID)
	if failure != nil {
		return *failure
	}
	if decision := p.Authorize(toConversation(chat)); !decision.Allowed {
		return rpc.Failure(req.ID, "not_visible", "conversation is not visible", false)
	}
	messages := make([]rpc.Message, 0, limit)
	complete := len(rows) < s.cfg.MaxMessageScan
	for _, row := range rows {
		message, include, err := s.exposeMessage(row, chat, p)
		if err != nil {
			return rpc.Failure(req.ID, "backend_invalid", "backend returned unsafe message metadata", false)
		}
		if include {
			if len(messages) == limit {
				complete = false
			} else {
				messages = append(messages, message)
			}
		}
	}
	return rpc.Success(req.ID, rpc.HistoryResult{Messages: messages, ScanComplete: complete})
}

func (s *Server) getMessage(ctx context.Context, req rpc.Request) rpc.Response {
	var params rpc.GetMessageParams
	if err := rpc.DecodeParams(req.Params, &params); err != nil {
		return invalidParams(req.ID, err)
	}
	if failure := s.verifyGeneration(req.ID, params.DatabaseGeneration); failure != nil {
		return *failure
	}
	if strings.TrimSpace(params.GUID) == "" || len(params.GUID) > 512 {
		return invalidParams(req.ID, fmt.Errorf("guid is required and must be at most 512 bytes"))
	}
	p, failure := s.loadPolicy(req.ID)
	if failure != nil {
		return *failure
	}
	chat, _, failure := s.authorizedChat(ctx, req.ID, params.ChatID, p)
	if failure != nil {
		return *failure
	}
	rows, err := s.deps.Backend.History(ctx, params.ChatID, s.cfg.MaxMessageScan)
	if err != nil {
		return backendFailure(req.ID, err)
	}
	p, failure = s.loadPolicy(req.ID)
	if failure != nil {
		return *failure
	}
	if decision := p.Authorize(toConversation(chat)); !decision.Allowed {
		return rpc.Failure(req.ID, "not_visible", "conversation is not visible", false)
	}
	for _, row := range rows {
		if row.GUID != params.GUID {
			continue
		}
		message, include, err := s.exposeMessage(row, chat, p)
		if err != nil {
			return rpc.Failure(req.ID, "backend_invalid", "backend returned unsafe message metadata", false)
		}
		if !include {
			return rpc.Failure(req.ID, "not_visible", "message is not visible", false)
		}
		return rpc.Success(req.ID, rpc.GetMessageResult{Message: message})
	}
	if len(rows) == s.cfg.MaxMessageScan {
		return rpc.Failure(req.ID, "lookup_incomplete", "message was not found within the bounded recent-history scan", false)
	}
	return rpc.Failure(req.ID, "not_found", "message was not found", false)
}

type cursor struct {
	V          int    `json:"v"`
	AccountID  string `json:"account_id"`
	Generation string `json:"generation"`
	RowID      int64  `json:"row_id"`
}

func encodeCursor(c cursor) string {
	payload, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(payload)
}

func decodeCursor(value string) (cursor, error) {
	if len(value) == 0 || len(value) > 2048 {
		return cursor{}, fmt.Errorf("invalid cursor")
	}
	payload, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return cursor{}, fmt.Errorf("invalid cursor")
	}
	var c cursor
	dec := json.NewDecoder(strings.NewReader(string(payload)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil || c.V != 1 || c.RowID <= 0 {
		return cursor{}, fmt.Errorf("invalid cursor")
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return cursor{}, fmt.Errorf("invalid cursor")
	}
	return c, nil
}

func (s *Server) collect(ctx context.Context, req rpc.Request) rpc.Response {
	var params rpc.CollectParams
	if err := rpc.DecodeParams(req.Params, &params); err != nil {
		return invalidParams(req.ID, err)
	}
	limit, err := normalizeLimit(params.Limit, 50, s.cfg.MaxResults)
	if err != nil {
		return invalidParams(req.ID, err)
	}
	var c cursor
	if params.Cursor != "" {
		if params.AfterRowID != 0 || params.DatabaseGeneration != "" {
			return invalidParams(req.ID, fmt.Errorf("cursor cannot be combined with after_row_id or database_generation"))
		}
		c, err = decodeCursor(params.Cursor)
		if err != nil {
			return invalidParams(req.ID, err)
		}
	} else {
		if params.AfterRowID <= 0 || params.DatabaseGeneration == "" {
			return invalidParams(req.ID, fmt.Errorf("cursor or positive after_row_id plus database_generation is required"))
		}
		c = cursor{V: 1, AccountID: s.cfg.AccountID, Generation: params.DatabaseGeneration, RowID: params.AfterRowID}
	}
	if c.AccountID != s.cfg.AccountID || c.Generation != s.deps.Backend.Generation() {
		return rpc.Failure(req.ID, "stale_cursor", "cursor belongs to a different account or database generation", false)
	}
	if _, failure := s.loadPolicy(req.ID); failure != nil {
		return *failure
	}
	rows, err := s.deps.Backend.Collect(ctx, c.RowID, s.cfg.MaxCollectionScan)
	if err != nil {
		return backendFailure(req.ID, err)
	}
	chatCache := map[int64]backend.RawChat{}
	previousRowID := c.RowID
	for _, row := range rows {
		if row.ID <= previousRowID {
			return rpc.Failure(req.ID, "backend_invalid", "backend returned non-monotonic incremental rows", false)
		}
		previousRowID = row.ID
		chat, ok := chatCache[row.ChatID]
		if !ok {
			chat, err = s.deps.Backend.Chat(ctx, row.ChatID)
			if err != nil {
				return backendFailure(req.ID, err)
			}
			if !validChat(chat) || chat.ID != row.ChatID {
				return rpc.Failure(req.ID, "backend_invalid", "backend returned invalid conversation metadata", false)
			}
			chatCache[row.ChatID] = chat
		}
	}
	// Load policy after every backend read so a concurrent revocation applies to
	// this response and no authorization cache survives between requests.
	p, failure := s.loadPolicy(req.ID)
	if failure != nil {
		return *failure
	}
	messages := make([]rpc.Message, 0, limit)
	lastScanned := c.RowID
	// Any observed batch gets one conservative follow-up probe. Upstream watch
	// exposes rows but not a definitive "caught up" marker.
	more := len(rows) > 0
	for _, row := range rows {
		chat := chatCache[row.ChatID]
		message, include, exposeErr := s.exposeMessage(row, chat, p)
		if exposeErr != nil {
			return rpc.Failure(req.ID, "backend_invalid", "backend returned unsafe message metadata", false)
		}
		if include && len(messages) == limit {
			more = true
			break
		}
		lastScanned = row.ID
		if include {
			messages = append(messages, message)
		}
	}
	c.RowID = lastScanned
	return rpc.Success(req.ID, rpc.CollectResult{Messages: messages, Cursor: encodeCursor(c), More: more})
}

func backendFailure(id string, err error) rpc.Response {
	if errors.Is(err, backend.ErrIncomplete) {
		return rpc.Failure(id, "collection_incomplete", "watch produced no rows; retry from the unchanged cursor", true)
	}
	if errors.Is(err, backend.ErrOverflow) {
		return rpc.Failure(id, "collection_overflow", "incremental backlog exceeds the configured safe scan bound", false)
	}
	return rpc.Failure(id, "backend_unavailable", "imsg backend request failed", true)
}
