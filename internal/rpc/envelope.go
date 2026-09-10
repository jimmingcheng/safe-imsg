package rpc

import "encoding/json"

const Version1 = 1

const (
	MethodSystemPing = "system.ping"
	MethodSystemInfo = "system.info"
	MethodListChats  = "imsg.list_chats"
	MethodHistory    = "imsg.history"
	MethodGetMessage = "imsg.get_message"
	MethodCollect    = "imsg.collect"
)

type Request struct {
	V      int             `json:"v"`
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

type Response struct {
	V      int        `json:"v"`
	ID     string     `json:"id"`
	OK     bool       `json:"ok"`
	Result any        `json:"result,omitempty"`
	Error  *ErrorBody `json:"error,omitempty"`
}

type ErrorBody struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func Success(id string, result any) Response {
	return Response{V: Version1, ID: id, OK: true, Result: result}
}

func Failure(id, code, message string, retryable bool) Response {
	return Response{V: Version1, ID: id, OK: false, Error: &ErrorBody{Code: code, Message: message, Retryable: retryable}}
}

type SystemInfo struct {
	Service            string              `json:"service"`
	ProtocolVersion    int                 `json:"protocol_version"`
	Instance           string              `json:"instance"`
	AccountID          string              `json:"account_id"`
	DatabaseGeneration string              `json:"database_generation"`
	MaxResults         int                 `json:"max_results"`
	Methods            []string            `json:"methods"`
	ContactsPolicy     *ContactsPolicyInfo `json:"contacts_policy,omitempty"`
}

// Health only: never disclose selected lists, contact identities or their counts.
type ContactsPolicyInfo struct {
	State         string `json:"state"`
	LastAttemptAt string `json:"last_attempt_at,omitempty"`
	LastSuccessAt string `json:"last_success_at,omitempty"`
	ExpiresAt     string `json:"expires_at,omitempty"`
	LastErrorCode string `json:"last_error_code,omitempty"`
}

type Chat struct {
	AccountID          string   `json:"account_id"`
	DatabaseGeneration string   `json:"database_generation"`
	ChatID             int64    `json:"chat_id"`
	ChatGUID           string   `json:"chat_guid"`
	Service            string   `json:"service"`
	IsGroup            bool     `json:"is_group"`
	Participants       []string `json:"participants"`
}

type Message struct {
	AccountID          string `json:"account_id"`
	DatabaseGeneration string `json:"database_generation"`
	RowID              int64  `json:"row_id"`
	ChatID             int64  `json:"chat_id"`
	ChatGUID           string `json:"chat_guid"`
	GUID               string `json:"guid"`
	Sender             string `json:"sender,omitempty"`
	FromMe             bool   `json:"from_me"`
	Text               string `json:"text"`
	CreatedAt          string `json:"created_at"`
}

type ListChatsParams struct {
	Limit int `json:"limit,omitempty"`
}

type GenerationParams struct {
	ChatID             int64  `json:"chat_id"`
	DatabaseGeneration string `json:"database_generation"`
	Limit              int    `json:"limit,omitempty"`
}

type GetMessageParams struct {
	ChatID             int64  `json:"chat_id"`
	DatabaseGeneration string `json:"database_generation"`
	GUID               string `json:"guid"`
}

type CollectParams struct {
	Cursor             string `json:"cursor,omitempty"`
	AfterRowID         int64  `json:"after_row_id,omitempty"`
	DatabaseGeneration string `json:"database_generation,omitempty"`
	Limit              int    `json:"limit,omitempty"`
}

type ListChatsResult struct {
	Chats        []Chat `json:"chats"`
	ScanComplete bool   `json:"scan_complete"`
}

type HistoryResult struct {
	Messages     []Message `json:"messages"`
	ScanComplete bool      `json:"scan_complete"`
}

type GetMessageResult struct {
	Message Message `json:"message"`
}

type CollectResult struct {
	Messages []Message `json:"messages"`
	Cursor   string    `json:"cursor"`
	More     bool      `json:"more"`
}
