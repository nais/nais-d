package debug

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/pterm/pterm"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"

	core_v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	debuggerSuffix               = "nais-debugger"
	debuggerContainerDefaultName = "debugger"
)

type Debug struct {
	ctx    context.Context
	client kubernetes.Interface
	cfg    *Config
}

type Config struct {
	Namespace    string
	Context      string
	WorkloadName string
	DebugImage   string
	CopyPod      bool
	ByPod        bool
}

func Setup(client kubernetes.Interface, cfg *Config) *Debug {
	return &Debug{
		ctx:    context.Background(),
		client: client,
		cfg:    cfg,
	}
}

func (d *Debug) getPodsForWorkload() (*core_v1.PodList, error) {
	pterm.Info.Println("Fetching workload...")
	var podList *core_v1.PodList
	var err error
	podList, err = d.client.CoreV1().Pods(d.cfg.Namespace).List(d.ctx, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("app.kubernetes.io/name=%s", d.cfg.WorkloadName),
	})
	if len(podList.Items) == 0 {
		podList, err = d.client.CoreV1().Pods(d.cfg.Namespace).List(d.ctx, metav1.ListOptions{
			LabelSelector: fmt.Sprintf("app=%s", d.cfg.WorkloadName),
		})
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get pods: %w", err)
	}
	return podList, nil
}

func debuggerContainerName(podName string) string {
	return fmt.Sprintf("%s-%s", podName, debuggerSuffix)
}

func (d *Debug) debugPod(podName string) error {
	const maxRetries = 6
	const pollInterval = 5

	if d.cfg.CopyPod {
		pN := debuggerContainerName(podName)
		_, err := d.client.CoreV1().Pods(d.cfg.Namespace).Get(d.ctx, pN, metav1.GetOptions{})
		if err == nil {
			pterm.Info.Printf("%s already exists, trying to attach...\n", pN)

			// Polling loop to check if the debugger container is running
			for i := 0; i < maxRetries; i++ {
				pterm.Info.Printf("Attempt %d/%d: Time remaining: %d seconds\n", i+1, maxRetries, (maxRetries-i)*pollInterval)
				pod, err := d.client.CoreV1().Pods(d.cfg.Namespace).Get(d.ctx, pN, metav1.GetOptions{})
				if err != nil {
					return fmt.Errorf("failed to get debug pod copy %s: %v", pN, err)
				}

				for _, c := range pod.Status.ContainerStatuses {
					if c.Name == debuggerContainerDefaultName && c.State.Running != nil {
						pterm.Success.Println("Container is running. Attaching...")
						return d.attachToExistingDebugContainer(pN)
					}
				}
				time.Sleep(time.Duration(pollInterval) * time.Second)
			}

			// If the loop finishes without finding the running container
			return fmt.Errorf("container did not start within the expected time")
		} else if !k8serrors.IsNotFound(err) {
			return fmt.Errorf("failed to check for existing debug pod copy %s: %v", pN, err)
		}
	} else {
		pod, err := d.client.CoreV1().Pods(d.cfg.Namespace).Get(d.ctx, podName, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("failed to get pod %s: %v", podName, err)
		}

		if len(pod.Spec.EphemeralContainers) > 0 {
			pterm.Warning.Printf("The container %s already has %d terminated debug containers.\n", podName, len(pod.Spec.EphemeralContainers))
			pterm.Info.Printf("Please consider using 'nais debug tidy %s' to clean up\n", d.cfg.WorkloadName)
		}
	}

	return d.createDebugPod(podName)
}

func (d *Debug) attachToExistingDebugContainer(podName string) error {
	cmd := exec.Command(
		"kubectl",
		"attach",
		"-n", d.cfg.Namespace,
		fmt.Sprintf("pod/%s", podName),
		"-c", debuggerContainerDefaultName,
		"-i",
		"-t",
	)

	if d.cfg.Context != "" {
		cmd.Args = append(cmd.Args, "--context", d.cfg.Context)
	}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start attach command: %v", err)
	}
	pterm.Success.Printf("Attached to pod %s\n", podName)

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("attach command failed: %v", err)
	}

	return nil
}

func (d *Debug) createDebugPod(podName string) error {
	args := []string{
		"debug",
		"-n", d.cfg.Namespace,
		fmt.Sprintf("pod/%s", podName),
		"-it",
		"--stdin",
		"--tty",
		"--profile=restricted",
		"-q",
		"--image", d.cfg.DebugImage,
	}

	if d.cfg.Context != "" {
		args = append(args, "--context", d.cfg.Context)
	}

	if d.cfg.CopyPod {
		args = append(args,
			"--copy-to", debuggerContainerName(podName),
			"-c", "debugger",
		)
	} else {
		args = append(args,
			"--target", d.cfg.WorkloadName)
	}

	cmd := exec.Command("kubectl", args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start debug command: %v", err)
	}

	if d.cfg.CopyPod {
		pterm.Info.Printf("Debugging pod copy created, enable process namespace sharing in %s\n", debuggerContainerName(podName))
	} else {
		pterm.Info.Println("Debugging container created...")
	}
	pterm.Info.Printf("Using debugger image %s\n", d.cfg.DebugImage)

	if err := cmd.Wait(); err != nil {
		if strings.Contains(err.Error(), "exit status 1") {
			pterm.Info.Println("Debugging container exited")
			return nil
		}
		return fmt.Errorf("debug command failed: %v", err)
	}

	if d.cfg.CopyPod {
		pterm.Info.Printf("Run 'nais debug -cp %s' command to attach to the debug pod\n", d.cfg.WorkloadName)
	}

	return nil
}

func (d *Debug) Debug() error {
	pods, err := d.getPodsForWorkload()
	if err != nil {
		return err
	}

	var podNames []string
	for _, pod := range pods.Items {
		podNames = append(podNames, pod.Name)
	}

	if len(podNames) == 0 {
		pterm.Info.Println("No pods found.")
		return nil
	}

	podName := podNames[0]
	if d.cfg.ByPod {
		result, err := pterm.DefaultInteractiveSelect.WithOptions(podNames).Show()
		if err != nil {
			pterm.Error.Printf("Prompt failed: %v\n", err)
			return err
		}
		podName = result
	}

	if err := d.debugPod(podName); err != nil {
		pterm.Error.Printf("Failed to debug pod %s: %v\n", podName, err)
	}

	return nil
}
