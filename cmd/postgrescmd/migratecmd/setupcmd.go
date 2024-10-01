package migratecmd

import (
	"context"
	"fmt"
	"strings"

	"github.com/nais/cli/pkg/k8s"
	"github.com/nais/cli/pkg/option"
	"github.com/nais/cli/pkg/postgres/migrate"
	"github.com/urfave/cli/v2"
)

const (
	tierFlagName     = "tier"
	diskSizeFlagName = "disk-size"
	typeFlagName     = "type"
)

func setupCommand() *cli.Command {
	return &cli.Command{
		Name:        "setup",
		Usage:       "Make necessary setup for a new migration",
		UsageText:   "nais postgres migrate setup APP_NAME NAMESPACE TARGET_INSTANCE_NAME",
		Description: "Setup will create a new (target) instance with updated configuration, and enable continuous replication of data from the source instance.",
		Args:        true,
		Flags: []cli.Flag{
			kubeConfigFlag(),
			&cli.StringFlag{
				Name:        tierFlagName,
				Usage:       "The `TIER` of the new instance",
				Category:    "Target instance configuration",
				EnvVars:     []string{"TARGET_INSTANCE_TIER"},
				DefaultText: "Source instance value",
				Action: func(context *cli.Context, v string) error {
					if !strings.HasPrefix(v, "db-") {
						return fmt.Errorf("tier must start with db-")
					}
					return nil
				},
			},
			&cli.IntFlag{
				Name:        diskSizeFlagName,
				Usage:       "The `DISK_SIZE` of the new instance",
				Category:    "Target instance configuration",
				EnvVars:     []string{"TARGET_INSTANCE_DISKSIZE"},
				DefaultText: "Source instance value",
			},
			&cli.StringFlag{
				Name:        typeFlagName,
				Usage:       "The `TYPE` of the new instance",
				Category:    "Target instance configuration",
				EnvVars:     []string{"TARGET_INSTANCE_TYPE"},
				DefaultText: "Source instance value",
				Action: func(context *cli.Context, v string) error {
					if !strings.HasPrefix(v, "POSTGRES_") {
						return fmt.Errorf("instance type must start with POSTGRES_")
					}
					return nil
				},
			},
			&cli.BoolFlag{
				Name:     "wait",
				Category: "UX",
				Usage:    "Wait for setup to complete",
			},
		},
		Before: beforeFunc,
		Action: func(cCtx *cli.Context) error {
			cfg := makeConfig(cCtx)

			cluster := cCtx.String(contextFlagName)
			tier := cCtx.String(tierFlagName)
			diskSize := cCtx.Int(diskSizeFlagName)
			instanceType := cCtx.String(typeFlagName)
			wait := cCtx.Bool("wait")

			fmt.Println(cCtx.Command.Description)
			cfg.Target.Tier = isSet(tier)
			cfg.Target.DiskSize = isSetInt(diskSize)
			cfg.Target.Type = isSet(instanceType)

			client := k8s.SetupClient(k8s.WithKubeContext(cluster))
			migrator := migrate.NewMigrator(client, cfg)

			err := migrator.Setup(context.Background(), wait)
			if err != nil {
				return fmt.Errorf("error setting up migration: %w", err)
			}
			return nil
		},
	}
}

func isSet(v string) option.Option[string] {
	if v == "" {
		return option.None[string]()
	}
	return option.Some(v)
}

func isSetInt(v int) option.Option[int] {
	if v == 0 {
		return option.None[int]()
	}
	return option.Some(v)
}
