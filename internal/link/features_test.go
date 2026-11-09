package link

import (
	"os"
	"strings"
	"testing"

	"github.com/vishvananda/netns"
)

func txChecksumActive(t *testing.T, ifName string, namespace netns.NsHandle) bool {
	t.Helper()

	var active bool
	err := withNetns(namespace, func() error {
		names, available, enabled, err := readNetdevFeatures(ifName)
		if err != nil {
			return err
		}
		for idx, name := range names {
			if !strings.HasPrefix(name, "tx-checksum-") {
				continue
			}
			if available[idx/32]&(1<<(idx%32)) == 0 {
				continue
			}
			if enabled[idx/32]&(1<<(idx%32)) != 0 {
				active = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unable to read netdev features: %v", err)
	}
	return active
}

func TestPrepareInterfacesDisablesTxChecksum(t *testing.T) {
	skipUnlessRoot(t)

	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	const ifName = "ethtest0"
	if _, err := CreateVethLink(ifName, ns, "ethtest1", ns); err != nil {
		t.Fatalf("unable to create veth: %v", err)
	}

	if !txChecksumActive(t, ifName, ns) {
		t.Skip("veth does not expose a changeable tx-checksum feature")
	}

	if err := PrepareInterfaces([]string{ifName}, ns, true, false); err != nil {
		t.Fatalf("PrepareInterfaces returned an error: %v", err)
	}
	if txChecksumActive(t, ifName, ns) {
		t.Fatal("tx checksum offload is still active")
	}

	// The operation must be idempotent; a second call has nothing to change.
	if err := PrepareInterfaces([]string{ifName}, ns, true, false); err != nil {
		t.Fatalf("second PrepareInterfaces returned an error: %v", err)
	}
}

func TestPrepareInterfacesWithoutWorkIsNoop(t *testing.T) {
	if err := PrepareInterfaces([]string{"ethtest0"}, netns.None(), false, false); err != nil {
		t.Fatalf("no-op PrepareInterfaces returned an error: %v", err)
	}
}

func TestPrepareInterfacesReportsUnknownInterface(t *testing.T) {
	skipUnlessRoot(t)

	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	if err := PrepareInterfaces([]string{"ethmissing0"}, ns, true, false); err == nil {
		t.Fatal("PrepareInterfaces unexpectedly succeeded on a missing interface")
	}
}

func TestPrepareInterfacesSkipsManagementNames(t *testing.T) {
	// Non-eth interfaces are management interfaces and must not be touched.
	if err := PrepareInterfaces([]string{"mgnt0"}, netns.None(), true, true); err != nil {
		t.Fatalf("PrepareInterfaces touched a management interface: %v", err)
	}
}

func TestPrepareInterfacesEnablesMplsInput(t *testing.T) {
	skipUnlessRoot(t)

	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	const ifName = "ethmpls0"
	if _, err := CreateVethLink(ifName, ns, "ethmpls1", ns); err != nil {
		t.Fatalf("unable to create veth: %v", err)
	}

	if err := PrepareInterfaces([]string{ifName}, ns, false, true); err != nil {
		t.Fatalf("PrepareInterfaces returned an error: %v", err)
	}

	var value []byte
	err := withNetns(ns, func() error {
		var err error
		value, err = os.ReadFile("/proc/sys/net/mpls/conf/" + ifName + "/input")
		return err
	})
	if err != nil {
		t.Fatalf("unable to read MPLS sysctl: %v", err)
	}
	if strings.TrimSpace(string(value)) != "1" {
		t.Fatalf("MPLS input = %q, want 1", value)
	}
}
