package node

func (n *Node) Cleanup() error {
	if n.virsh != nil {
		return n.virsh.Close()
	}
	return nil
}
