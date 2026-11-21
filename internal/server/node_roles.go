package server

import (
	"context"
	"io"

	"github.com/moby/term"
	"github.com/satcomprojects/gonetem/internal/link"
	"github.com/vishvananda/netns"
)

// Node roles are declared by the consumers that need them. Implementations in
// internal/docker, internal/namespace and internal/ovs satisfy these
// implicitly; they do not import this package.
//
// Keep roles narrow. New behaviour should be added to the smallest role that
// needs it, not to the Node composite.

type NodeIdentity interface {
	GetName() string
	GetShortName() string
	GetType() string
	GetFullType() string
}

type NodeLifecycle interface {
	IsRunning() bool
	Start() error
	Stop() error
	Kill() error
	Close() error
}

type NodeNetwork interface {
	GetNetns() (netns.NsHandle, error)
	GetInterfaceName(ifIndex int) string
	AttachMgntInterface(ifName string, ns netns.NsHandle, ipAddress string) error
	AttachInterface(ifName string, ifIndex int, configure bool) error
	DetachInterface(ifName string) error
	ConfigureInterfaces() error
	GetInterfacesState() map[string]link.IfState
	SetInterfaceState(ifIndex int, state link.IfState) error
}

type NodeExec interface {
	ExecCommand(ctx context.Context, cmd []string, in io.ReadCloser, out io.Writer, tty bool, ttyHeight uint, ttyWidth uint, resizeCh chan term.Winsize) error
	RunCommand(cmd []string) ([]byte, []byte, int, error)
	GetConsoleCmd(shell bool) ([]string, error)
	Capture(ctx context.Context, ifIndex int, out io.Writer) error
}

type NodePersistence interface {
	LoadConfig(confPath string, timeout int) ([]string, error)
	ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error)
	Save(dstPath string, timeout int) error
	CopyFrom(srcPath, destPath string) error
	CopyTo(srcPath, destPath string) error
}

// Node is the composite used by the topology engine and node factory. Keep
// this interface stable; consumers should accept the narrow roles above.
type Node interface {
	NodeIdentity
	NodeLifecycle
	NodeNetwork
	NodeExec
	NodePersistence
}

// LinkNode is the subset needed to create, configure and remove links.
type LinkNode interface {
	NodeIdentity
	GetNetns() (netns.NsHandle, error)
	GetInterfaceName(ifIndex int) string
	AttachMgntInterface(ifName string, ns netns.NsHandle, ipAddress string) error
	AttachInterface(ifName string, ifIndex int, configure bool) error
	DetachInterface(ifName string) error
}

// NodeNetns is the subset needed to open a node network namespace.
type NodeNetns interface {
	GetName() string
	GetNetns() (netns.NsHandle, error)
}

// RouteNode is the subset needed to install routes.
type RouteNode interface {
	GetName() string
	GetType() string
	IsRunning() bool
	GetNetns() (netns.NsHandle, error)
}

// StartableNode is the subset needed to start and configure a node.
type StartableNode interface {
	GetName() string
	Start() error
	ConfigureInterfaces() error
	LoadConfig(confPath string, timeout int) ([]string, error)
}

// StopableNode is the subset needed to stop a node.
type StopableNode interface {
	GetName() string
	Stop() error
}

// NodeStatus is the subset needed to report node status.
type NodeStatus interface {
	GetName() string
	IsRunning() bool
	GetInterfacesState() map[string]link.IfState
}

// Handler roles.

type InterfaceStateNode interface {
	NodeIdentity
	SetInterfaceState(ifIndex int, state link.IfState) error
}

type CaptureNode interface {
	NodeIdentity
	Capture(ctx context.Context, ifIndex int, out io.Writer) error
}

type ExecNode interface {
	NodeIdentity
	ExecCommand(ctx context.Context, cmd []string, in io.ReadCloser, out io.Writer, tty bool, ttyHeight uint, ttyWidth uint, resizeCh chan term.Winsize) error
}

type ConsoleNode interface {
	NodeIdentity
	GetConsoleCmd(shell bool) ([]string, error)
}

type RunNode interface {
	NodeIdentity
	RunCommand(cmd []string) ([]byte, []byte, int, error)
}

type CopyNode interface {
	NodeIdentity
	CopyFrom(srcPath, destPath string) error
	CopyTo(srcPath, destPath string) error
}
