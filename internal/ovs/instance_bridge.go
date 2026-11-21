package ovs

import "fmt"

// findBr returns the index of brName in the instance bridge list, or -1.
// The caller must hold o.mu for reading or writing.
func (o *OvsProjectInstance) findBr(brName string) int {
	for idx, br := range o.bridges {
		if br == brName {
			return idx
		}
	}
	return -1
}

func (o *OvsProjectInstance) AddBr(brName string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.findBr(brName) != -1 {
		return fmt.Errorf("switch %s already exists", brName)
	}

	cmd := []string{"ovs-vsctl", "add-br", brName}
	if err := o.Exec(cmd); err != nil {
		return err
	}
	o.bridges = append(o.bridges, brName)

	return nil
}

func (o *OvsProjectInstance) DelBr(brName string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	brIdx := o.findBr(brName)
	if brIdx != -1 {
		cmd := []string{"ovs-vsctl", "del-br", brName}
		if err := o.Exec(cmd); err != nil {
			return err
		}

		// remove br from list
		o.bridges[brIdx] = o.bridges[len(o.bridges)-1]
		o.bridges = o.bridges[:len(o.bridges)-1]
	}

	return nil
}

func (o *OvsProjectInstance) AddBridgeWithPorts(brName string, ifNames []string) error {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.findBr(brName) != -1 {
		return fmt.Errorf("switch %s already exists", brName)
	}

	cmd := []string{"ovs-vsctl", "--may-exist", "add-br", brName}
	for _, ifName := range ifNames {
		cmd = append(cmd, "--", "--may-exist", "add-port", brName, ifName)
	}
	if err := o.Exec(cmd); err != nil {
		return err
	}

	o.bridges = append(o.bridges, brName)
	return nil
}

func (o *OvsProjectInstance) AddPort(brName, ifName string) error {
	cmd := []string{"ovs-vsctl", "add-port", brName, ifName}
	return o.Exec(cmd)
}

func (o *OvsProjectInstance) DelPort(brName, ifName string) error {
	cmd := []string{"ovs-vsctl", "del-port", brName, ifName}
	return o.Exec(cmd)
}
