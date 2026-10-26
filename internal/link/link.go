package link

import (
	"errors"
	"fmt"
	"net"
	"os"
	"runtime"

	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

type IfState int

const (
	IFSTATE_UP IfState = iota
	IFSTATE_DOWN
)

func GetRootNetns() netns.NsHandle {
	ns, _ := netns.GetFromPid(os.Getpid())

	return ns
}

// withNetns runs fn with the current OS thread switched to ns. The original
// namespace is restored before the thread is returned to the Go scheduler.
func withNetns(ns netns.NsHandle, fn func() error) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	orig, err := netns.Get()
	if err != nil {
		return fmt.Errorf("unable to get current netns: %w", err)
	}
	defer orig.Close()

	if err := netns.Set(ns); err != nil {
		return fmt.Errorf("unable to switch to netns: %w", err)
	}
	defer func() {
		if err := netns.Set(orig); err != nil {
			logger.Errorf("unable to restore original netns: %v", err)
		}
	}()

	return fn()
}

func withNetlinkHandle(ns netns.NsHandle, fn func(*netlink.Handle) error) error {
	handle, err := netlink.NewHandleAt(ns, unix.NETLINK_ROUTE)
	if err != nil {
		return fmt.Errorf("unable to open netlink handle: %w", err)
	}
	defer handle.Close()

	return fn(handle)
}

func IsLinkExist(name string, namespace netns.NsHandle) bool {
	err := withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		_, err := handle.LinkByName(name)
		return err
	})
	return err == nil
}

func CreateVethLink(name string, namespace netns.NsHandle, peerName string, peerNamespace netns.NsHandle) (*netlink.Veth, error) {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name:      name,
			MTU:       1500,
			TxQLen:    1000,
			Namespace: netlink.NsFd(namespace),
		},
		PeerName:      peerName,
		PeerNamespace: netlink.NsFd(peerNamespace),
	}

	// Both endpoint namespaces are explicit in the attributes, so the veth can
	// be created from the current (root) namespace without a setns round trip.
	if err := netlink.LinkAdd(veth); err != nil {
		return nil, fmt.Errorf("error when creating Veth: %v", err)
	}

	return veth, nil
}

func CreateNetns(name string) (netns.NsHandle, error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	orig, err := netns.Get()
	if err != nil {
		return netns.None(), fmt.Errorf("unable to get current netns: %w", err)
	}
	defer orig.Close()

	ns, err := netns.NewNamed(name)
	restoreErr := netns.Set(orig)
	if err != nil {
		return netns.None(), err
	}
	if restoreErr != nil {
		ns.Close()
		return netns.None(), fmt.Errorf("unable to restore original netns: %w", restoreErr)
	}

	return ns, nil
}

func DeleteNetns(name string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	return netns.DeleteNamed(name)
}

func CreateBridge(name string, namespace netns.NsHandle) (*netlink.Bridge, error) {
	la := netlink.NewLinkAttrs()
	la.Name = name
	la.Namespace = netlink.NsFd(namespace)
	br := &netlink.Bridge{LinkAttrs: la}

	if err := withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		if err := handle.LinkAdd(br); err != nil {
			return fmt.Errorf("error when creating bridge %s: %v", name, err)
		}
		if err := handle.LinkSetUp(br); err != nil {
			return fmt.Errorf("error when set %s up: %v", name, err)
		}
		return nil
	}); err != nil {
		return br, err
	}

	return br, nil
}

func CreateMacVlan(name string, parent string, peerMAC net.HardwareAddr, mode netlink.MacvlanMode, namespace netns.NsHandle) (*netlink.Macvlan, error) {
	macvlan := &netlink.Macvlan{}

	if err := withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		parentLink, err := handle.LinkByName(parent)
		if err != nil {
			return fmt.Errorf("unable to find macvlan parent %s: %v", parent, err)
		}

		la := netlink.NewLinkAttrs()
		la.Name = name
		la.Namespace = netlink.NsFd(namespace)
		la.ParentIndex = parentLink.Attrs().Index
		la.HardwareAddr = peerMAC
		macvlan.LinkAttrs = la
		macvlan.Mode = mode

		if err := handle.LinkAdd(macvlan); err != nil {
			return fmt.Errorf("error when creating MACVLAN %s: %v", name, err)
		}
		return nil
	}); err != nil {
		return macvlan, err
	}

	return macvlan, nil
}

func CreateVRRPMacVlan(name string, parent string, group int, namespace netns.NsHandle) (*netlink.Macvlan, error) {
	peerMAC, err := net.ParseMAC(fmt.Sprintf("00:00:5E:00:01:%02X", group))
	if err != nil {
		return nil, err
	}

	return CreateMacVlan(name, parent, peerMAC, netlink.MACVLAN_MODE_BRIDGE, namespace)
}

func CreateVrf(name string, namespace netns.NsHandle, table int) (*netlink.Vrf, error) {
	la := netlink.NewLinkAttrs()
	la.Name = name
	la.Namespace = netlink.NsFd(namespace)
	vrf := &netlink.Vrf{LinkAttrs: la, Table: uint32(table)}

	if err := withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		if err := handle.LinkAdd(vrf); err != nil {
			return fmt.Errorf("error when creating VRF %s: %v", name, err)
		}
		if err := handle.LinkSetUp(vrf); err != nil {
			return fmt.Errorf("error when set %s up: %v", name, err)
		}
		return nil
	}); err != nil {
		return vrf, err
	}

	return vrf, nil
}

func AttachToBridge(br *netlink.Bridge, ifName string, namespace netns.NsHandle) error {
	return withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		ifObj, err := handle.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("unable to get %s: %v", ifName, err)
		}
		return handle.LinkSetMaster(ifObj, br)
	})
}

func DeleteLink(name string, namespace netns.NsHandle) error {
	return withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		l, err := handle.LinkByName(name)
		if err != nil {
			return fmt.Errorf("unable to get link %s: %v", name, err)
		}
		return handle.LinkDel(l)
	})
}

// DeleteLinks deletes several links while entering the namespace only once.
// All deletions are attempted and their errors are aggregated.
func DeleteLinks(names []string, namespace netns.NsHandle) error {
	if len(names) == 0 {
		return nil
	}

	return withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		var errs []error
		for _, name := range names {
			l, err := handle.LinkByName(name)
			if err != nil {
				errs = append(errs, fmt.Errorf("unable to get link %s: %w", name, err))
				continue
			}
			if err := handle.LinkDel(l); err != nil {
				errs = append(errs, fmt.Errorf("unable to delete link %s: %w", name, err))
			}
		}
		return errors.Join(errs...)
	})
}

func RenameLink(name string, target string, namespace netns.NsHandle) error {
	return withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		l, err := handle.LinkByName(name)
		if err != nil {
			return fmt.Errorf("renameLink - Unable get link %s: %v", name, err)
		}

		if err := handle.LinkSetName(l, target); err != nil {
			return fmt.Errorf("error when renaming link %s->%s: %v", name, target, err)
		}
		if err := handle.LinkSetUp(l); err != nil {
			return fmt.Errorf("error when set %s up: %v", name, err)
		}
		return nil
	})
}

func SetInterfaceState(name string, namespace netns.NsHandle, state IfState) error {
	return withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		l, err := handle.LinkByName(name)
		if err != nil {
			return fmt.Errorf("unable get link %s: %v", name, err)
		}

		switch state {
		case IFSTATE_UP:
			if err := handle.LinkSetUp(l); err != nil {
				return fmt.Errorf("error when set %s up: %v", name, err)
			}
		case IFSTATE_DOWN:
			if err := handle.LinkSetDown(l); err != nil {
				return fmt.Errorf("error when set %s down: %v", name, err)
			}
		}
		return nil
	})
}

func SetLinkNetns(link netlink.Link, targetNs netns.NsHandle) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	return netlink.LinkSetNsFd(link, int(targetNs))
}

func MoveInterfacesNetns(ifNames map[string]IfState, current netns.NsHandle, target netns.NsHandle) error {
	if len(ifNames) == 0 {
		return nil
	}

	return withNetlinkHandle(current, func(handle *netlink.Handle) error {
		for ifName := range ifNames {
			l, err := handle.LinkByName(ifName)
			if err != nil {
				return fmt.Errorf("unable get link %s: %v", ifName, err)
			}

			if err := handle.LinkSetNsFd(l, int(target)); err != nil {
				return fmt.Errorf("error when update netns for %s: %v", ifName, err)
			}
		}
		return nil
	})
}

func IpAddressAdd(ifName string, namespace netns.NsHandle, IPAddress string) error {
	return withNetlinkHandle(namespace, func(handle *netlink.Handle) error {
		l, err := handle.LinkByName(ifName)
		if err != nil {
			return fmt.Errorf("unable get link %s: %v", ifName, err)
		}

		addr, err := netlink.ParseAddr(IPAddress)
		if err != nil {
			return fmt.Errorf("unable to parse IP address %s: %v", IPAddress, err)
		}

		if err := handle.AddrAdd(l, addr); err != nil {
			return fmt.Errorf("unable to add IP address %s to link %s: %v", IPAddress, ifName, err)
		}
		return nil
	})
}
