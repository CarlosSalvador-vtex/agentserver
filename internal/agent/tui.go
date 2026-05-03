package agent

import (
	"context"
	"errors"
)

type TUIOpts struct {
	Server          string
	WorkspaceID     string
	Name            string
	WorkDir         string
	Resume          string
	Continue        bool
	Yolo            bool
	SkipOpenBrowser bool
	Model           string
	ResponderTTL    string
}

// RunTUI is the entry point for the `tui` subcommand. Stubbed in Task 1;
// completed in Task 14 once Model + Bus + AuthController are wired.
func RunTUI(ctx context.Context, opts TUIOpts) error {
	return errors.New("tui: not yet implemented (Task 14 will wire it)")
}
