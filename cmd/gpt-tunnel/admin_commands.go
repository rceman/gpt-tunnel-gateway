package main

import (
	"context"

	"github.com/rceman/gpt-tunnel-gateway/internal/config"
	"github.com/rceman/gpt-tunnel-gateway/internal/service"
)

type adminSessionMintRequest struct {
	Label *string `json:"label,omitempty"`
}

type adminSessionRevokeRequest struct {
	Session string `json:"session"`
}

func admin(ctx context.Context, c config.Config, args []string) {
	require(args, 2)
	if args[0] != "session" {
		usage()
	}
	switch args[1] {
	case "mint":
		label, rest := stringFlag("--label", args[2:])
		if len(rest) != 0 {
			usage()
		}
		result, err := operatorCLIRequest[service.AdminSessionResult](ctx, c, "/operator/admin/session/mint", adminSessionMintRequest{Label: optionalString(label)})
		if err != nil {
			fatal(err)
		}
		output(result)
	case "revoke":
		if len(args) != 3 {
			usage()
		}
		result, err := operatorCLIRequest[service.AdminSessionResult](ctx, c, "/operator/admin/session/revoke", adminSessionRevokeRequest{Session: args[2]})
		if err != nil {
			fatal(err)
		}
		output(result)
	default:
		usage()
	}
}

func stringFlag(name string, args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1], append(args[:i], args[i+2:]...)
		}
	}
	return "", args
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
