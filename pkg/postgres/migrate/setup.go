package migrate

import (
	"context"
	"fmt"
	"github.com/pterm/pterm"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/rand"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

const SetupSuccessMessage = `
Migration setup has been started successfully.

To monitor the migration, run the following command:
	kubectl logs -f -l %s -n %s

The setup will take some time to complete, you can check completion status with the following command:
	kubectl get job %s -n %s

When setup is complete, a new instance has been created and replication of data has started.
You can check the replication progress in the Google Cloud Console:
	%s

When the migration has Status Running and is in the CDC or Ready to Promote phase,
you can proceed with the next step of the migration:
	nais postgres migrate promote %s %s %s

Be aware that during promotion (the next step), your instance will be unavailable for some time.
`

func (m *Migrator) Setup(ctx context.Context, wait bool) error {
	cfgMapList := &v1.ConfigMapList{}
	listOptions := []client.ListOption{
		client.InNamespace(m.cfg.Namespace),
		client.MatchingLabels{"migrator.nais.io/app-name": m.cfg.AppName},
	}
	err := m.client.List(ctx, cfgMapList, listOptions...)
	if err != nil {
		return err
	}

	if len(cfgMapList.Items) > 0 {
		return fmt.Errorf("migration config already exists for this application")
	}

	fmt.Println("Resolving target instance config")
	err = m.cfg.Target.Resolve(ctx, m.client, m.cfg.AppName, m.cfg.Namespace)
	if err != nil {
		return err
	}
	fmt.Printf("Resolved target:\n%s\n", m.cfg.Target.String())

	fmt.Println("Resolving source instance config")
	err = m.cfg.Source.Resolve(ctx, m.client, m.cfg.AppName, m.cfg.Namespace)
	if err != nil {
		return err
	}
	fmt.Printf("Resolved source:\n%s\n", m.cfg.Source.String())

	sourceInstanceName := m.cfg.Source.InstanceName.String()
	if sourceInstanceName == "" {
		return fmt.Errorf("source instance name is empty")
	}

	targetInstanceName := m.cfg.Target.InstanceName.String()
	if targetInstanceName == "" {
		return fmt.Errorf("target instance name is required")
	}

	if sourceInstanceName == targetInstanceName {
		return fmt.Errorf("source and target instance names cannot be the same")
	}

	fmt.Println("Looking up GCP project ID")
	gcpProjectId, err := m.LookupGcpProjectId(ctx)
	if err != nil {
		return fmt.Errorf("failed to lookup GCP project ID: %w", err)
	}

	m.printConfig()
	fmt.Print(`
This will create a new database instance and start replication of data from the source instance.
`)
	err = confirmContinue()
	if err != nil {
		return err
	}

	//fmt.Println("Creating ConfigMap")
	//cfgMap := m.cfg.CreateConfigMap()
	//err = m.client.Create(ctx, cfgMap)
	//if err != nil {
	//	return fmt.Errorf("failed to create ConfigMap: %w", err)
	//}
	//
	//fmt.Println("Creating RoleBinding")
	//roleBinding := makeRoleBinding(m.cfg)
	//err = createObject(ctx, m, cfgMap, roleBinding, CommandSetup)
	//if err != nil {
	//	return err
	//}
	//
	//jobName, err := m.doNaisJob(ctx, cfgMap, CommandSetup)
	//if err != nil {
	//	return err
	//}

	ptermFailed := false
	if wait {
		err = m.doSpinner()
		if err != nil {
			fmt.Println("Failed to start spinner:", err)
			ptermFailed = true
		}
	}

	if !wait || ptermFailed {
		cloudConsoleUrl := fmt.Sprintf("https://console.cloud.google.com/dbmigration/migrations/locations/europe-north1/instances/%s-%s?project=%s", m.cfg.Source.InstanceName, m.cfg.Target.InstanceName, gcpProjectId)
		label := m.kubectlLabelSelector(CommandSetup)
		fmt.Printf(SetupSuccessMessage, label, m.cfg.Namespace, "jobName", m.cfg.Namespace, cloudConsoleUrl, m.cfg.AppName, m.cfg.Namespace, m.cfg.Target.InstanceName)
	}
	return nil
}

func (m *Migrator) doSpinner() error {
	multi := pterm.DefaultMultiPrinter

	logStatements := make(chan string)

	go func() {
		for i := 0; i < 50; i++ {
			delay := rand.IntnRange(5, 10)
			time.Sleep(time.Second * time.Duration(delay))
			logStatements <- fmt.Sprintf("Migration setup is still running, waited %d seconds", delay)
		}
		close(logStatements)
	}()

	logOutput := pterm.DefaultLogger.WithWriter(multi.NewWriter())
	logOutput.Info("Migration setup has started")
	setupSpinner, err := pterm.DefaultSpinner.
		WithWriter(multi.NewWriter()).
		WithSequence("  .  ", " ... ", ".....", " ... ").
		Start("Setting up migration")
	if err != nil {
		return fmt.Errorf("failed to start spinner: %w", err)
	}

	_, err = multi.Start()
	if err != nil {
		return fmt.Errorf("failed to start multi printer: %w", err)
	}
	defer multi.Stop()

	prevLog := ""
	for logMsg := range logStatements {
		switch rand.IntnRange(1, 3) {
		case 1:
			logOutput.Warn(prevLog)
		case 2:
			logOutput.Error(prevLog)
		default:
			logOutput.Info(prevLog)
		}
		setupSpinner.UpdateText(logMsg)
		prevLog = logMsg
	}
	setupSpinner.Success("Migration setup complete")
	return nil
}
