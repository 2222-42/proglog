package proglog

import (
	"github.com/2222-42/proglog/internal/agent"
	"github.com/2222-42/proglog/internal/config"
	"github.com/spf13/cobra"
	"log"
)

func main() {
	cli := &cli{}

	cmd := &cobra.Command{
		Use:     "proglog",
		PreRunE: cli.setupConfig,
		RunE:    cli.run,
	}

	if err := setupFlags(cmd); err != nil {
		log.Fatal(err)
	}

	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}

type cli struct {
	cfg cfg
}

type cfg struct {
	agent.Config
	ServerTLSConfig config.TLSConfig
	PeerTLSConfig   config.TLSConfig
}

func setupFlags(cmd *cobra.Command) error {
	return nil
}

func (c *cli) setupConfig(cmd *cobra.Command, args []string) error {
	return nil
}

func (c *cli) run(cmd *cobra.Command, args []string) error {
	return nil
}
