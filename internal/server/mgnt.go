package server

import (
	"fmt"
	"sync"

	"github.com/mroy31/gonetem/internal/link"
	"github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
)

type MgntNetwork struct {
	NetId      string
	IPAddress  string
	Instance   *netlink.Bridge
	NetNs      netns.NsHandle
	Interfaces []string
	Logger     *logrus.Entry
	mu         sync.Mutex
}

func (mNet *MgntNetwork) Create() error {
	var err error

	mNet.Instance, err = link.CreateBridge(mNet.NetId, mNet.NetNs)
	if err != nil {
		return fmt.Errorf("unable to create management bridge %s: %v", mNet.NetId, err)
	}

	if err := link.IpAddressAdd(mNet.NetId, mNet.NetNs, mNet.IPAddress); err != nil {
		return fmt.Errorf("unable to assign IP address %s to mgnt network: %v", mNet.IPAddress, err)
	}

	return nil
}

func (mNet *MgntNetwork) AttachInterface(ifName string) error {
	if mNet.Instance == nil {
		return fmt.Errorf("mgnt network is not created")
	}

	if err := link.AttachToBridge(mNet.Instance, ifName, mNet.NetNs); err != nil {
		return fmt.Errorf("unable to attach %s to mgnt network: %v", ifName, err)
	}

	mNet.mu.Lock()
	mNet.Interfaces = append(mNet.Interfaces, ifName)
	mNet.mu.Unlock()

	return nil
}

func (mNet *MgntNetwork) DetachInterface(ifName string) error {
	if mNet.Instance == nil {
		return nil
	}

	if err := link.DeleteLink(ifName, mNet.NetNs); err != nil {
		return fmt.Errorf("unable to detach %s from mgnt network: %v", ifName, err)
	}

	mNet.mu.Lock()
	for i, name := range mNet.Interfaces {
		if name == ifName {
			mNet.Interfaces = append(mNet.Interfaces[:i], mNet.Interfaces[i+1:]...)
			break
		}
	}
	mNet.mu.Unlock()

	return nil
}

func (mNet *MgntNetwork) Close() error {
	mNet.mu.Lock()
	interfaces := append([]string(nil), mNet.Interfaces...)
	mNet.Interfaces = make([]string, 0)
	mNet.mu.Unlock()

	// A node whose netns was destroyed during its own Close() already
	// removed the peer side of its management veth. Only delete the links
	// that still exist in the root namespace.
	existing := make([]string, 0, len(interfaces))
	for _, ifName := range interfaces {
		if link.IsLinkExist(ifName, mNet.NetNs) {
			existing = append(existing, ifName)
		}
	}
	if err := link.DeleteLinks(existing, mNet.NetNs); err != nil {
		mNet.Logger.Warnf("Error when deleting management links: %v", err)
	}

	var err error
	if mNet.Instance != nil {
		err = link.DeleteLink(mNet.NetId, mNet.NetNs)
	}

	if mNet.NetNs != 0 {
		if closeErr := mNet.NetNs.Close(); err == nil {
			err = closeErr
		}
		mNet.NetNs = 0
	}

	return err
}
