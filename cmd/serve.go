package cmd

import (
	"fmt"
	"log"
	"os/signal"
	"syscall"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/socketmode"
	"github.com/spf13/cobra"

	"github.com/nlink-jp/ir-hub/internal/acl"
	"github.com/nlink-jp/ir-hub/internal/analysis"
	"github.com/nlink-jp/ir-hub/internal/bot"
	"github.com/nlink-jp/ir-hub/internal/cases"
	"github.com/nlink-jp/ir-hub/internal/config"
	"github.com/nlink-jp/ir-hub/internal/export"
	"github.com/nlink-jp/ir-hub/internal/ingest"
	"github.com/nlink-jp/ir-hub/internal/llm"
	"github.com/nlink-jp/ir-hub/internal/msg"
	"github.com/nlink-jp/ir-hub/internal/slackapi"
	"github.com/nlink-jp/ir-hub/internal/storage"
	"github.com/nlink-jp/ir-hub/internal/store"
	"github.com/nlink-jp/ir-hub/internal/userdir"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Run the resident Slack bot (Socket Mode)",
	Long: `serve connects to Slack over Socket Mode and stays resident,
handling /ir-hub commands, @ir-hub mentions, and case-channel
message ingestion.

Requires IRHUB_SLACK_APP_TOKEN (app-level, connections:write) and
IRHUB_SLACK_BOT_TOKEN in the environment.`,
	Args: cobra.NoArgs,
	RunE: runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(cmd *cobra.Command, _ []string) error {
	// Fail fast on configuration problems before touching Slack.
	cfg, err := config.Load(flagConfig)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintln(cmd.ErrOrStderr(), w)
	}
	if err := cfg.ValidateServe(); err != nil {
		return err
	}

	st, err := store.Open(cfg.DB.Path)
	if err != nil {
		return err
	}
	defer st.Close()

	client := slack.New(cfg.Slack.BotToken, slack.OptionAppLevelToken(cfg.Slack.AppToken))
	api := slackapi.NewAdapter(client)

	ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	ident, err := api.AuthTest(ctx)
	if err != nil {
		return fmt.Errorf("slack auth test: %w", err)
	}
	log.Printf("serve: authenticated as %s (%s)", ident.User, ident.UserID)

	checker := acl.New(acl.Config{
		AllowUsers:  cfg.ACL.AllowUsers,
		AllowGroups: cfg.ACL.AllowGroups,
		DenyUsers:   cfg.ACL.DenyUsers,
		DenyGroups:  cfg.ACL.DenyGroups,
		CacheTTL:    time.Duration(cfg.ACL.GroupCacheTTL) * time.Second,
	}, api)
	// Unknown group handles are config typos: refuse to start.
	if err := checker.ValidateGroups(ctx); err != nil {
		return err
	}

	catalog := msg.For(cfg.Language)
	// Resolves Slack user IDs to "display name (ID)" in status,
	// postmortems, and knowledge documents.
	resolver := userdir.New(api)
	caseSvc := cases.New(api, st, cases.Config{
		DefaultVisibility: cfg.Channel.DefaultVisibility,
		NamePrefix:        cfg.Channel.NamePrefix,
		Msg:               catalog,
	}, cases.WithResolver(resolver))
	ing := ingest.New(api, st)

	llmClient, err := llm.NewVertex(ctx, cfg.GCP.Project, cfg.GCP.Location, cfg.Model.Name,
		cfg.Analysis.RequestTimeout)
	if err != nil {
		return err
	}
	runner := analysis.NewRunner(llmClient, st, analysis.Config{
		Language:       cfg.Language,
		BotUserID:      ident.UserID,
		MaxInputTokens: cfg.Analysis.MaxInputTokens,
	}, analysis.WithResolver(resolver))

	// Postmortem runs interrupted by a previous shutdown stay
	// 'running' forever otherwise.
	if n, err := st.FailStaleRuns(); err != nil {
		return err
	} else if n > 0 {
		log.Printf("serve: marked %d stale postmortem run(s) as failed", n)
	}

	// Knowledge export backend. A cloud client that can't initialize
	// (missing credentials, off-cloud) degrades gracefully: export
	// is disabled, the bot keeps running.
	var botOpts []bot.Option
	if backend, err := storage.New(ctx, cfg.Storage); err != nil {
		log.Printf("serve: storage backend %q unavailable, export disabled: %v", cfg.Storage.Backend, err)
	} else {
		botOpts = append(botOpts, bot.WithExport(export.New(st, backend)))
		log.Printf("serve: knowledge export enabled (%s)", backend.Name())
	}

	b := bot.New(bot.NewSocketAdapter(socketmode.New(client)), api, st, checker, caseSvc, ing, runner,
		bot.Config{
			DefaultVisibility: cfg.Channel.DefaultVisibility,
			NotifyDenied:      cfg.ACL.NotifyDenied,
			Msg:               catalog,
			BotUserID:         ident.UserID,
		}, botOpts...)

	log.Printf("serve: ir-hub %s starting (db: %s, model: %s)", rootCmd.Version, cfg.DB.Path, cfg.Model.Name)
	if err := b.Run(ctx); err != nil && ctx.Err() == nil {
		return fmt.Errorf("socket mode: %w", err)
	}
	log.Printf("serve: shutting down, waiting for in-flight work")
	b.Wait()
	log.Printf("serve: shutdown complete")
	return nil
}
