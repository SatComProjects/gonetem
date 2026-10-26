package link

import (
	"net"
	"os"
	"runtime"
	"testing"

	"github.com/mroy31/gonetem/internal/utils"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

func skipUnlessRoot(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Test requires root privileges.")
	}
}

func setUpNetlinkTest(t *testing.T) (netns.NsHandle, func()) {
	skipUnlessRoot(t)

	// new temporary namespace so we don't pollute the host
	// lock thread since the namespace is thread local
	runtime.LockOSThread()
	var err error
	ns, err := netns.New()
	if err != nil {
		t.Fatal("Failed to create newns", ns)
	}

	return ns, func() {
		ns.Close()
		runtime.UnlockOSThread()
	}
}

func checkLinkExistence(t *testing.T, lkNames ...string) {
	for _, lkName := range lkNames {
		_, err := netlink.LinkByName(lkName)
		if err != nil {
			t.Fatalf("Unable to find created vrf: %v", lkName)
		}
	}
}

func TestWithNetnsRestoresOriginalNamespace(t *testing.T) {
	skipUnlessRoot(t)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	orig, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer orig.Close()

	target, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := netns.Set(orig); err != nil {
		t.Fatal(err)
	}

	if err := withNetns(target, func() error {
		current, err := netns.Get()
		if err != nil {
			return err
		}
		defer current.Close()
		if !current.Equal(target) {
			t.Errorf("closure did not run in target namespace")
		}
		return nil
	}); err != nil {
		t.Fatalf("withNetns returned an error: %v", err)
	}

	after, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer after.Close()
	if !after.Equal(orig) {
		t.Fatal("withNetns did not restore the original namespace")
	}
}

func TestLink_CreateVeth(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	veth, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatalf("Unable to create veth: %v", err)
	}
	defer netlink.LinkDel(veth)

	checkLinkExistence(t, veth.Name, veth.PeerName)
}

func TestCreateVethLinkAcrossNamespaces(t *testing.T) {
	skipUnlessRoot(t)

	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	orig, err := netns.Get()
	if err != nil {
		t.Fatal(err)
	}
	defer orig.Close()

	target, err := netns.New()
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()

	if err := netns.Set(orig); err != nil {
		t.Fatal(err)
	}

	name := utils.RandString(6)
	peer := utils.RandString(6)
	veth, err := CreateVethLink(name, target, peer, target)
	if err != nil {
		t.Fatalf("Unable to create cross-namespace veth: %v", err)
	}
	defer DeleteLink(veth.Name, target)

	err = withNetlinkHandle(target, func(handle *netlink.Handle) error {
		if _, err := handle.LinkByName(name); err != nil {
			return err
		}
		_, err := handle.LinkByName(peer)
		return err
	})
	if err != nil {
		t.Fatalf("veth endpoints were not created in the target namespace: %v", err)
	}
}

func TestLink_CreateBridge(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	br, err := CreateBridge(utils.RandString(5), ns)
	if err != nil {
		t.Fatalf("Unable to create veth: %v", err)
	}
	defer netlink.LinkDel(br)

	checkLinkExistence(t, br.Name)
}

func TestLink_CreateVRRPMacvlan(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	parent := &netlink.Dummy{LinkAttrs: netlink.LinkAttrs{Name: "foo"}}
	if err := netlink.LinkAdd(parent); err != nil {
		t.Fatal(err)
	}
	defer netlink.LinkDel(parent)

	macvlan, err := CreateVRRPMacVlan(utils.RandString(5), "foo", 1, ns)
	if err != nil {
		t.Fatalf("Unable to create macvlan: %v", err)
	}
	defer netlink.LinkDel(macvlan)

	checkLinkExistence(t, macvlan.Name)
}

func TestLink_CreateVrf(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	vrf, err := CreateVrf(utils.RandString(6), ns, 10)
	if err != nil {
		t.Fatalf("Unable to create VRF: %v", err)
	}
	defer netlink.LinkDel(vrf)

	checkLinkExistence(t, vrf.Name)
}

func TestLink_DeleteLinksBatches(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	v1, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatal(err)
	}
	v2, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatal(err)
	}

	if err := DeleteLinks([]string{v1.Name, v2.Name}, ns); err != nil {
		t.Fatalf("DeleteLinks returned an error: %v", err)
	}

	for _, name := range []string{v1.Name, v1.PeerName, v2.Name, v2.PeerName} {
		if _, err := netlink.LinkByName(name); err == nil {
			t.Fatalf("link %s still exists after DeleteLinks", name)
		}
	}
}

func TestLink_SetPeerInterfaceUp(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	veth, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatalf("Unable to create veth: %v", err)
	}
	defer netlink.LinkDel(veth)

	if err := SetInterfaceState(veth.PeerName, ns, IFSTATE_UP); err != nil {
		t.Fatalf("Unable to set peer interface up: %v", err)
	}

	peer, err := netlink.LinkByName(veth.PeerName)
	if err != nil {
		t.Fatal(err)
	}
	if peer.Attrs().Flags&net.FlagUp == 0 {
		t.Fatalf("peer interface %s is not up", veth.PeerName)
	}
}

func TestLink_InterfaceState(t *testing.T) {
	ns, teardown := setUpNetlinkTest(t)
	defer teardown()

	veth, err := CreateVethLink(utils.RandString(6), ns, utils.RandString(6), ns)
	if err != nil {
		t.Fatalf("Unable to create veth: %v", err)
	}
	defer netlink.LinkDel(veth)

	checkLinkExistence(t, veth.Name, veth.PeerName)

	// set interface up
	if err := SetInterfaceState(veth.Name, ns, IFSTATE_UP); err != nil {
		t.Fatalf("%v", err)
	}
}
