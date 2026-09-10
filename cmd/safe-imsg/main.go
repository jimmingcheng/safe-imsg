package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/rpc"
	"github.com/jimmingcheng/safe-imsg/internal/version"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 1 && args[0] == "version" {
		fmt.Printf("safe-imsg %s (%s)\n", version.Version, version.Commit)
		return 0
	}
	global := flag.NewFlagSet("safe-imsg", flag.ContinueOnError)
	global.SetOutput(os.Stderr)
	socket := global.String("socket", "", "broker Unix socket path")
	if err := global.Parse(args); err != nil {
		return 2
	}
	rest := global.Args()
	if *socket == "" || len(rest) == 0 {
		usage()
		return 2
	}
	method, params, ok := parseCommand(rest)
	if !ok {
		return 2
	}
	payload, err := json.Marshal(params)
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not encode request")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	resp, err := rpc.Call(ctx, *socket, rpc.Request{V: rpc.Version1, ID: requestID(), Method: method, Params: payload})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !resp.OK {
		if resp.Error == nil {
			fmt.Fprintln(os.Stderr, "broker returned an invalid error response")
			return 1
		}
		fmt.Fprintf(os.Stderr, "%s: %s\n", resp.Error.Code, resp.Error.Message)
		return 1
	}
	result, err := json.MarshalIndent(resp.Result, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not decode broker result")
		return 1
	}
	fmt.Println(string(result))
	return 0
}

func parseCommand(args []string) (string, any, bool) {
	switch args[0] {
	case "ping":
		if len(args) != 1 {
			usage()
			return "", nil, false
		}
		return rpc.MethodSystemPing, struct{}{}, true
	case "info":
		if len(args) != 1 {
			usage()
			return "", nil, false
		}
		return rpc.MethodSystemInfo, struct{}{}, true
	case "chats":
		flags := commandFlags("chats")
		limit := flags.Int("limit", 0, "maximum chats")
		if !parseFlags(flags, args[1:]) {
			return "", nil, false
		}
		return rpc.MethodListChats, rpc.ListChatsParams{Limit: *limit}, true
	case "history":
		flags := commandFlags("history")
		chatID := flags.Int64("chat-id", 0, "database-local chat row id")
		generation := flags.String("database-generation", "", "generation from info or chats")
		limit := flags.Int("limit", 0, "maximum messages")
		if !parseFlags(flags, args[1:]) {
			return "", nil, false
		}
		return rpc.MethodHistory, rpc.GenerationParams{ChatID: *chatID, DatabaseGeneration: *generation, Limit: *limit}, true
	case "message":
		flags := commandFlags("message")
		chatID := flags.Int64("chat-id", 0, "database-local chat row id")
		generation := flags.String("database-generation", "", "generation from info or chats")
		guid := flags.String("guid", "", "stable message GUID")
		if !parseFlags(flags, args[1:]) {
			return "", nil, false
		}
		return rpc.MethodGetMessage, rpc.GetMessageParams{ChatID: *chatID, DatabaseGeneration: *generation, GUID: *guid}, true
	case "collect":
		flags := commandFlags("collect")
		cursor := flags.String("cursor", "", "cursor returned by collect")
		after := flags.Int64("after-row-id", 0, "known row id for the first collection")
		generation := flags.String("database-generation", "", "generation for first collection")
		limit := flags.Int("limit", 0, "maximum messages")
		notBefore := flags.String("not-before", "", "RFC3339 horizon for first collection")
		if !parseFlags(flags, args[1:]) {
			return "", nil, false
		}
		return rpc.MethodCollect, rpc.CollectParams{Cursor: *cursor, AfterRowID: *after, DatabaseGeneration: *generation, Limit: *limit, NotBefore: *notBefore}, true
	default:
		usage()
		return "", nil, false
	}
}

func commandFlags(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	return flags
}

func parseFlags(flags *flag.FlagSet, args []string) bool {
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return false
	}
	return true
}

func requestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err == nil {
		return hex.EncodeToString(value[:])
	}
	return strconv.FormatInt(time.Now().UnixNano(), 10)
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: safe-imsg --socket PATH <ping|info|chats|history|message|collect> [flags]")
}
