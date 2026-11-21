package namespace

func (n *NamespaceNode) LoadConfig(confPath string, timeout int) ([]string, error) {
	return nil, nil
}

func (n *NamespaceNode) ReadConfigFiles(confDir string, timeout int) (map[string][]byte, error) {
	return make(map[string][]byte), nil
}

func (n *NamespaceNode) Save(dstPath string, timeout int) error {
	return nil
}
