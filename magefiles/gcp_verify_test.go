// Copyright (c) 2026 Mesh Intelligence Inc.
// SPDX-License-Identifier: MIT-0

package main

import (
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

// The kind gates keep their ingress hosts: nothing the GKE verbs add changes
// what app:verify and app:verifyTrace address by default.
func TestKindVerifySeamsNameTheIngressHosts(t *testing.T) {
	if got, want := demoVerifyURL(), "http://"+cohereApplicationHost+"/api/v1/chat"; got != want {
		t.Errorf("demoVerifyURL() = %q, want %q", got, want)
	}
	if got, want := demoObserverOrigin, "http://"+cohereObserverHost; got != want {
		t.Errorf("demoObserverOrigin = %q, want %q", got, want)
	}
}

// The GKE verbs point the kubeconfig seam at the cluster's private
// kubeconfig, and restoring puts back exactly what was there.
func TestGcpPointKubeconfigRestoresTheKindSeam(t *testing.T) {
	original := applicationKubeconfig
	t.Cleanup(func() { applicationKubeconfig = original })
	applicationKubeconfig = func() (string, error) { return "kind-sentinel", nil }

	restore := gcpPointKubeconfig("/private/gke/config")
	if got, _ := applicationKubeconfig(); got != "/private/gke/config" {
		t.Fatalf("the seam names %q while a GKE gate runs", got)
	}
	args, err := applicationKubectlArgs("get", "pods")
	if err != nil || strings.Join(args, " ") != "--kubeconfig /private/gke/config --namespace "+cohereApplicationNamespace+" get pods" {
		t.Fatalf("kubectl argv = %v, %v", args, err)
	}
	restore()
	if got, _ := applicationKubeconfig(); got != "kind-sentinel" {
		t.Fatalf("restore left the seam at %q", got)
	}
}

func TestGcpPointOriginRestores(t *testing.T) {
	before := demoChatOrigin
	restore := gcpPointOrigin(&demoChatOrigin, "http://127.0.0.1:41000")
	if got := demoVerifyURL(); got != "http://127.0.0.1:41000/api/v1/chat" {
		t.Errorf("demoVerifyURL() = %q while forwarded", got)
	}
	restore()
	if demoChatOrigin != before {
		t.Errorf("restore left demoChatOrigin at %q", demoChatOrigin)
	}
}

// Each forward names a Service and a port the GKE render actually carries,
// so a chart rename fails here rather than in a billed session.
func TestGcpForwardsNameRenderedServicePorts(t *testing.T) {
	_, output := renderGcpRelease(t)
	for _, forward := range []gcpForward{gcpChatForward(), gcpObserverForward()} {
		var found bool
		for _, document := range strings.Split(output, "\n---") {
			if strings.Contains(document, "\nkind: Service\n") &&
				strings.Contains(document, "\n  name: "+forward.Service+"\n") {
				found = true
				if !strings.Contains(document, "{name: "+forward.Port+",") {
					t.Errorf("Service %s has no port named %s", forward.Service, forward.Port)
				}
			}
		}
		if !found {
			t.Errorf("the GKE render has no Service %s", forward.Service)
		}
	}
	if gcpChatForward().Origin != &demoChatOrigin || gcpObserverForward().Origin != &demoObserverOrigin {
		t.Error("a forward carries the wrong origin seam")
	}
}

func TestGcpAwaitListener(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	if err := gcpAwaitListener(listener.Addr().String(), make(chan error, 1), time.Second); err != nil {
		t.Errorf("a listening address reported %v", err)
	}

	port, err := gcpFreePort()
	if err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	exited <- errors.New("exit status 1")
	err = gcpAwaitListener(net.JoinHostPort("127.0.0.1", itoa(port)), exited, 5*time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited before the forward listened") {
		t.Errorf("an exited forward reported %v", err)
	}
	if got := <-exited; got == nil {
		t.Error("gcpAwaitListener consumed the exit result the stop function needs")
	}
}
