package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/jimmingcheng/safe-imsg/internal/config"
	"github.com/jimmingcheng/safe-imsg/internal/contacts"
	"github.com/jimmingcheng/safe-imsg/internal/policy"
)

func contactsCommand(args []string) int {
	if len(args) == 0 || (args[0] != "groups" && args[0] != "preview") {
		usage()
		return 2
	}
	flags := flag.NewFlagSet("contacts "+args[0], flag.ContinueOnError)
	helper := flags.String("helper", "", "trusted native helper executable (groups only)")
	configPath := flags.String("config", "", "owner broker config (preview only)")
	show := flags.Bool("show-identities", false, "print the effective phone/email whitelist locally")
	if flags.Parse(args[1:]) != nil || flags.NArg() != 0 {
		return 2
	}
	if args[0] == "groups" && (*helper == "" || *configPath != "" || *show) {
		return 2
	}
	if args[0] == "preview" && (*configPath == "" || *helper != "") {
		return 2
	}
	var result any
	var err error
	if args[0] == "groups" {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		result, err = contacts.Discover(ctx, *helper)
	} else {
		result, err = previewContacts(*configPath, *show)
	}
	if err != nil {
		// Owner CLI may explain fixed errors, but never relay native stderr or
		// malformed helper output (which may contain the entire address book).
		result = map[string]any{"status": "error", "code": contacts.ErrorCode(err)}
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if encodeErr := encoder.Encode(result); encodeErr != nil {
		fmt.Fprintln(os.Stderr, "could not write Contacts result")
		return 1
	}
	if err != nil {
		return 1
	}
	return 0
}

func previewContacts(path string, show bool) (any, error) {
	cfg, err := config.Load(path)
	if err != nil || cfg.Contacts == nil {
		return nil, contacts.InvalidConfig
	}
	base, err := policy.Load(cfg.PolicyPath)
	if err != nil {
		return nil, contacts.InvalidConfig
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.Contacts.TimeoutMillis)*time.Millisecond)
	defer cancel()
	snapshot, err := contacts.Fetch(ctx, *cfg.Contacts)
	if err != nil {
		return nil, err
	}
	identities, skipped, err := contacts.Identities(*cfg.Contacts, snapshot)
	if err != nil {
		return nil, err
	}
	effective, err := base.WithDirect(identities)
	if err != nil {
		return nil, contacts.Invalid
	}
	result := map[string]any{"status": "ok", "activated": false, "contact_count": len(snapshot.Contacts),
		"effective_direct_identity_count": len(effective.DirectIdentities()), "skipped_field_count": skipped,
		"container_id": cfg.Contacts.ContainerID, "group_ids": snapshot.GroupIDs,
		"all_lists":               cfg.Contacts.GroupIDs == nil,
		"upstream_sync_freshness": "unknown"}
	if show {
		result["allowed_direct"] = effective.DirectIdentities()
	}
	return result, nil
}
