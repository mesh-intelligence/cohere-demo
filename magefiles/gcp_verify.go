// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// The GKE seed and verify gates (GH-880). Each runs the kind gate's own logic
// against the release gcp:deploy installed: the application kubeconfig seam
// names the environment's cluster, and the chat and observer origins name
// port-forwards to its Services. The port-forwards reach the mesh behind IAP
// on purpose -- the gates prove the mesh, and the edge is proven by its own
// checks -- so no identity token is minted and nothing is read from the
// environment (eng07).

// Seed brings the GKE release from an empty corpus to a grounded state, as
// app:seed does on kind.
func (Gcp) Seed() error {
	return gcpOnApplication("gcp:seed", nil, App{}.Seed)
}

// Verify serves one tasking against the GKE release and asserts the answer
// composed from retrieved documents, as app:verify does on kind.
func (Gcp) Verify() error {
	return gcpOnApplication("gcp:verify", []gcpForward{gcpChatForward()}, App{}.Verify)
}

// VerifyTrace runs one research turn against the GKE release and proves the
// DefTech ask against the turn's own trace, as app:verifyTrace does on kind.
func (Gcp) VerifyTrace() error {
	return gcpOnApplication("gcp:verifyTrace", []gcpForward{gcpChatForward(), gcpObserverForward()}, App{}.VerifyTrace)
}

// Exporter proves the brief export against the GKE release, as
// integration:exporter does on kind (srd013 R6.5).
func (Gcp) Exporter() error {
	return gcpOnApplication("gcp:exporter", []gcpForward{gcpChatForward()}, func() error {
		return proveExporter(demoChatOrigin)
	})
}

// gcpForward is one Service port a gate reaches through a port-forward, and
// the origin seam that carries the local address while the gate runs.
type gcpForward struct {
	Service string
	Port    string
	Origin  *string
}

func gcpChatForward() gcpForward {
	return gcpForward{Service: gcpServiceName("chatbot"), Port: "chat", Origin: &demoChatOrigin}
}

func gcpObserverForward() gcpForward {
	return gcpForward{Service: gcpServiceName("observer"), Port: "monitor", Origin: &demoObserverOrigin}
}

// gcpServiceName is the Service the chart names for one component of the
// release.
func gcpServiceName(component string) string {
	return fmt.Sprintf("%s-%s-%s", cohereApplicationRelease, demoChartName, component)
}

// gcpOnApplication points the application seams at the environment's
// cluster, opens the port-forwards, runs gate, logs its outcome as the verb's
// phase, and restores every seam and stops every forward on each return path.
func gcpOnApplication(verb string, forwards []gcpForward, gate func() error) error {
	run, err := newGcpRun(verb)
	if err != nil {
		return err
	}
	config, _, err := run.deployConfig()
	if err != nil {
		return err
	}
	dir, err := os.MkdirTemp("", "cohere-demo-gcp-kubeconfig-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	kubeconfig := filepath.Join(dir, "config")
	if err := writeGcpKubeconfig(config, kubeconfig); err != nil {
		return err
	}
	defer gcpPointKubeconfig(kubeconfig)()
	for _, forward := range forwards {
		origin, stop, err := gcpPortForward(forward.Service, forward.Port)
		if err != nil {
			return err
		}
		defer stop()
		defer gcpPointOrigin(forward.Origin, origin)()
	}
	started := time.Now()
	return gcpLogOutcome(config.Cluster, strings.TrimPrefix(verb, "gcp:"), "passed", started, gate())
}

// gcpPointKubeconfig makes every application kubectl and helm call name
// kubeconfig, and returns the function that restores the kind seam.
func gcpPointKubeconfig(kubeconfig string) (restore func()) {
	previous := applicationKubeconfig
	applicationKubeconfig = func() (string, error) { return kubeconfig, nil }
	return func() { applicationKubeconfig = previous }
}

// gcpPointOrigin sets one origin seam and returns the function that restores
// it.
func gcpPointOrigin(seam *string, origin string) (restore func()) {
	previous := *seam
	*seam = origin
	return func() { *seam = previous }
}

// gcpPortForward forwards a free local port to the named port of a Service in
// the application namespace and returns the local origin once it accepts
// connections. The returned stop ends the forward.
func gcpPortForward(service, port string) (string, func(), error) {
	local, err := gcpFreePort()
	if err != nil {
		return "", nil, err
	}
	args, err := applicationKubectlArgs("port-forward", "svc/"+service, fmt.Sprintf("%d:%s", local, port))
	if err != nil {
		return "", nil, err
	}
	cmd := exec.Command("kubectl", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return "", nil, fmt.Errorf("port-forward svc/%s: %w", service, err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stop := func() {
		_ = cmd.Process.Kill()
		<-exited
		exited <- nil
	}
	address := fmt.Sprintf("127.0.0.1:%d", local)
	if err := gcpAwaitListener(address, exited, 30*time.Second); err != nil {
		stop()
		return "", nil, fmt.Errorf("port-forward svc/%s %s: %w", service, port, err)
	}
	return "http://" + address, stop, nil
}

// gcpAwaitListener waits until address accepts a connection, failing early
// when the forward exits first.
func gcpAwaitListener(address string, exited chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		select {
		case err := <-exited:
			exited <- err
			return errors.Join(errors.New("kubectl exited before the forward listened"), err)
		default:
		}
		if conn, err := net.DialTimeout("tcp", address, time.Second); err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s did not listen within %s", address, timeout)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

// gcpFreePort asks the kernel for an unused loopback port.
func gcpFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}
