package link

import (
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"
)

const (
	// ethSSFeatures is ETH_SS_FEATURES from linux/ethtool.h. It is not
	// exported by x/sys/unix because userspace normally goes through ethtool.
	ethSSFeatures = 4
	// ethGStringLen is ETH_GSTRING_LEN: every feature name occupies a fixed
	// 32-byte slot in the ETHTOOL_GSTRINGS reply.
	ethGStringLen = 32
	// ethFeaturesMaxBlocks mirrors ETHTOOL_FEATURES_MAX_BLOCKS. The kernel
	// rejects bigger requests, and 128 blocks cover 4096 netdev features.
	ethFeaturesMaxBlocks = 128
)

// PrepareInterfaces applies the interface-level settings gonetem needs on a
// Docker node without spawning a helper process in the container. It switches
// to namespace once and then updates each eth* interface directly.
//
// disableTso clears the device TX checksum offloads (the equivalent of
// "ethtool -K <if> tx off") and enableMpls enables MPLS input on the interface.
func PrepareInterfaces(ifNames []string, namespace netns.NsHandle, disableTso, enableMpls bool) error {
	if !disableTso && !enableMpls {
		return nil
	}

	names := make([]string, 0, len(ifNames))
	for _, ifName := range ifNames {
		if strings.HasPrefix(ifName, "eth") {
			names = append(names, ifName)
		}
	}
	if len(names) == 0 {
		return nil // only management interfaces
	}

	return withNetns(namespace, func() error {
		var errs []error
		for _, ifName := range names {
			if disableTso {
				if err := disableTxChecksumOffload(ifName); err != nil {
					errs = append(errs, fmt.Errorf("unable to disable TSO on %s: %w", ifName, err))
				}
			}
			if enableMpls {
				if err := enableMplsInput(ifName); err != nil {
					errs = append(errs, fmt.Errorf("unable to enable MPLS input on %s: %w", ifName, err))
				}
			}
		}
		return errors.Join(errs...)
	})
}

// enableMplsInput writes net.mpls.conf.<if>.input=1. The sysctl is per network
// namespace, so /proc/sys/net resolves to the namespace selected by withNetns.
func enableMplsInput(ifName string) error {
	path := filepath.Join("/proc/sys/net/mpls/conf", ifName, "input")
	if err := os.WriteFile(path, []byte("1"), 0644); err != nil {
		return err
	}
	return nil
}

// disableTxChecksumOffload clears every changeable tx-checksum-* netdev
// feature. This is what ethtool's "tx" feature group does for a veth pair.
func disableTxChecksumOffload(ifName string) error {
	names, available, _, err := readNetdevFeatures(ifName)
	if err != nil {
		return err
	}

	blocks := (len(names) + 31) / 32
	if blocks == 0 {
		return nil
	}

	valid := make([]uint32, blocks)
	changed := false
	for idx, name := range names {
		if !strings.HasPrefix(name, "tx-checksum-") {
			continue
		}
		if available[idx/32]&(1<<(idx%32)) == 0 {
			continue // feature is fixed and cannot be changed
		}
		valid[idx/32] |= 1 << (idx % 32)
		changed = true
	}
	if !changed {
		return nil
	}

	buf := make([]byte, 8+blocks*8)
	binary.LittleEndian.PutUint32(buf[0:], unix.ETHTOOL_SFEATURES)
	binary.LittleEndian.PutUint32(buf[4:], uint32(blocks))
	for idx, mask := range valid {
		// requested stays zero: every valid feature is disabled.
		binary.LittleEndian.PutUint32(buf[8+idx*8:], mask)
	}
	return ethtoolIoctl(ifName, unsafe.Pointer(&buf[0]))
}

// readNetdevFeatures returns the ETH_SS_FEATURES names and the matching
// changeable-feature masks from ETHTOOL_GFEATURES.
func readNetdevFeatures(ifName string) ([]string, []uint32, []uint32, error) {
	count, err := readFeatureCount(ifName)
	if err != nil {
		return nil, nil, nil, err
	}
	if count == 0 {
		return nil, nil, nil, nil
	}

	names, err := readFeatureNames(ifName, count)
	if err != nil {
		return nil, nil, nil, err
	}

	blocks := (count + 31) / 32
	buf := make([]byte, 8+blocks*16)
	binary.LittleEndian.PutUint32(buf[0:], unix.ETHTOOL_GFEATURES)
	binary.LittleEndian.PutUint32(buf[4:], uint32(blocks))
	if err := ethtoolIoctl(ifName, unsafe.Pointer(&buf[0])); err != nil {
		return nil, nil, nil, err
	}
	// The kernel may return fewer blocks than requested, or resize the reply.
	returned := int(binary.LittleEndian.Uint32(buf[4:]))
	if returned > blocks {
		return nil, nil, nil, fmt.Errorf("invalid feature block count %d", returned)
	}

	available := make([]uint32, blocks)
	active := make([]uint32, blocks)
	for idx := 0; idx < returned; idx++ {
		available[idx] = binary.LittleEndian.Uint32(buf[8+idx*16:])
		active[idx] = binary.LittleEndian.Uint32(buf[8+idx*16+8:])
	}
	return names, available, active, nil
}

func readFeatureCount(ifName string) (int, error) {
	// struct ethtool_sset_info { u32 cmd; u32 reserved; u64 sset_mask; u32 data[0]; }
	buf := make([]byte, 16+4)
	binary.LittleEndian.PutUint32(buf[0:], unix.ETHTOOL_GSSET_INFO)
	binary.LittleEndian.PutUint64(buf[8:], 1<<ethSSFeatures)
	if err := ethtoolIoctl(ifName, unsafe.Pointer(&buf[0])); err != nil {
		return 0, err
	}
	count := int(binary.LittleEndian.Uint32(buf[16:]))
	if count < 0 || count > ethFeaturesMaxBlocks*32 {
		return 0, fmt.Errorf("invalid netdev feature count %d", count)
	}
	return count, nil
}

func readFeatureNames(ifName string, count int) ([]string, error) {
	if count > ethFeaturesMaxBlocks*32 {
		return nil, fmt.Errorf("too many netdev features: %d", count)
	}

	// struct ethtool_gstrings { u32 cmd; u32 string_set; u32 len; u8 data[0]; }
	buf := make([]byte, 12+count*ethGStringLen)
	binary.LittleEndian.PutUint32(buf[0:], unix.ETHTOOL_GSTRINGS)
	binary.LittleEndian.PutUint32(buf[4:], ethSSFeatures)
	binary.LittleEndian.PutUint32(buf[8:], uint32(count))
	if err := ethtoolIoctl(ifName, unsafe.Pointer(&buf[0])); err != nil {
		return nil, err
	}

	names := make([]string, count)
	for idx := 0; idx < count; idx++ {
		slot := string(buf[12+idx*ethGStringLen : 12+(idx+1)*ethGStringLen])
		if end := strings.IndexByte(slot, 0); end >= 0 {
			slot = slot[:end]
		}
		names[idx] = slot
	}
	return names, nil
}

// ethtoolIoctl issues SIOCETHTOOL for ifName with data as the command payload.
// The ifreq layout depends on the architecture, so reuse the size of the
// x/sys/unix Ifreq type instead of hard-coding it.
func ethtoolIoctl(ifName string, data unsafe.Pointer) error {
	ifr, err := unix.NewIfreq(ifName)
	if err != nil {
		return err
	}

	buf := make([]byte, unsafe.Sizeof(*ifr))
	copy(buf[:unix.IFNAMSIZ], ifName)
	*(*unsafe.Pointer)(unsafe.Pointer(&buf[unix.IFNAMSIZ])) = data

	fd, err := unix.Socket(unix.AF_INET, unix.SOCK_DGRAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(fd)

	_, _, errno := unix.Syscall(
		unix.SYS_IOCTL,
		uintptr(fd),
		uintptr(unix.SIOCETHTOOL),
		uintptr(unsafe.Pointer(&buf[0])),
	)
	runtime.KeepAlive(buf)
	runtime.KeepAlive(data)
	if errno != 0 {
		return errno
	}
	return nil
}
