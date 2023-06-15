package mq

import (
	"encoding/json"
	"fmt"
	"net"

	"github.com/gravitl/netmaker/logger"
	"github.com/gravitl/netmaker/logic"
	"github.com/gravitl/netmaker/logic/acls/nodeacls"
	"github.com/gravitl/netmaker/models"
	"github.com/gravitl/netmaker/servercfg"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// PubPeerUpdate publishes a peer update to the client
// relay is set to a newly created relay node or nil for other peer updates
func PubPeerUpdate(client, relay *models.Client, peers []models.Client) {
	p := models.PeerAction{
		Action: models.UpdatePeer,
	}
	if client.Node.IsRelay {
		pubRelayUpdate(client, peers)
		return
	}
	if relay != nil {
		if client.Node.RelayedBy == relay.Node.ID.String() {
			pubRelayedUpdate(client, relay, peers)
			return
		}
	}
	for _, peer := range peers {
		if client.Host.ID == peer.Host.ID {
			continue
		}
		update := wgtypes.PeerConfig{
			PublicKey:         peer.Host.PublicKey,
			ReplaceAllowedIPs: true,
			Endpoint: &net.UDPAddr{
				IP:   peer.Host.EndpointIP,
				Port: peer.Host.ListenPort,
			},
			PersistentKeepaliveInterval: &peer.Node.PersistentKeepalive,
		}
		if nodeacls.AreNodesAllowed(nodeacls.NetworkID(client.Node.Network), nodeacls.NodeID(client.Node.ID.String()), nodeacls.NodeID(peer.Node.ID.String())) {
			update.AllowedIPs = append(update.AllowedIPs, logic.AddAllowedIPs(&peer)...)
		} else {
			update.Remove = true
		}
		if relay != nil {
			if peer.Node.IsRelayed && peer.Node.RelayedBy == relay.Node.ID.String() {
				update.Remove = true
			}
		}
		if peer.Node.IsRelay {
			update.AllowedIPs = append(update.AllowedIPs, getRelayAllowedIPs(*client, peer)...)
		}
		p.Peers = append(p.Peers, update)
	}
	data, err := json.Marshal(p)
	if err != nil {
		logger.Log(0, "marshal peer update", err.Error())
		return
	}
	publish(&client.Host, fmt.Sprintf("peer/host/%s/%s", client.Host.ID.String(), servercfg.GetServer()), data)
}

// getRelayAllowedIPs returns the list of allowedips for a given peer that is a relay
func getRelayAllowedIPs(client, peer models.Client) []net.IPNet {
	var relayIPs []net.IPNet
	for _, relayed := range peer.Node.RelayedNodes {
		node, err := logic.GetNodeByID(relayed)
		if err != nil {
			logger.Log(0, "retrieve relayed node", err.Error())
			continue
		}
		if !nodeacls.AreNodesAllowed(nodeacls.NetworkID(client.Node.Network), nodeacls.NodeID(client.Node.ID.String()), nodeacls.NodeID(node.ID.String())) {
			continue
		}
		if node.Address.IP != nil {
			node.Address.Mask = net.CIDRMask(32, 32)
			relayIPs = append(relayIPs, node.Address)
		}
		if node.Address6.IP != nil {
			node.Address.Mask = net.CIDRMask(128, 128)
			relayIPs = append(relayIPs, node.Address6)
		}
		if node.IsRelay {
			relayIPs = append(relayIPs, getRelayAllowedIPs(client, peer)...)
		}
		if node.IsEgressGateway {
			relayIPs = append(relayIPs, getEgressIPs(peer)...)
		}
		if node.IsIngressGateway {
			relayIPs = append(relayIPs, getIngressIPs(peer)...)
		}
	}
	return relayIPs
}

// getEgressIPs returns the additional allowedips (egress ranges) that need
// to be included for an egress gateway peer
func getEgressIPs(peer models.Client) []net.IPNet {
	var egressIPs []net.IPNet
	for _, egressRange := range peer.Node.EgressGatewayRanges {
		ip, cidr, err := net.ParseCIDR(egressRange)
		if err != nil {
			logger.Log(0, "parse egress range", err.Error())
			continue
		}
		cidr.IP = ip
		egressIPs = append(egressIPs, *cidr)
	}
	return egressIPs
}

// getIngressIPs returns the additional allowedips (ext client addresses) that need
// to be included for an ingress gateway peer
// TODO:  add ExtraAllowedIPs
func getIngressIPs(peer models.Client) []net.IPNet {
	var ingressIPs []net.IPNet
	extclients, err := logic.GetNetworkExtClients(peer.Node.Network)
	if err != nil {
		return ingressIPs
	}
	for _, ec := range extclients {
		if ec.IngressGatewayID == peer.Node.ID.String() {
			if ec.Address != "" {
				ip, cidr, err := net.ParseCIDR(ec.Address)
				if err != nil {
					continue
				}
				cidr.IP = ip
				ingressIPs = append(ingressIPs, *cidr)
			}
			if ec.Address6 != "" {
				ip, cidr, err := net.ParseCIDR(ec.Address6)
				if err != nil {
					continue
				}
				cidr.IP = ip
				ingressIPs = append(ingressIPs, *cidr)
			}
		}
	}
	return ingressIPs
}

// pubRelayedUpdate - publish peer update to a node (client) that is relayed by the relay
func pubRelayedUpdate(client, relay *models.Client, peers []models.Client) {
	//verify
	if !logic.StringSliceContains(relay.Node.RelayedNodes, client.Node.ID.String()) {
		logger.Log(0, "invalid call to pubRelayed update", client.Host.Name, relay.Host.Name)
		return
	}
	//remove all nodes except relay
	p := models.PeerAction{
		Action: models.RemovePeer,
	}
	for _, peer := range peers {
		if peer.Host.ID == relay.Host.ID || peer.Host.ID == client.Host.ID {
			continue
		}
		update := wgtypes.PeerConfig{
			PublicKey: peer.Host.PublicKey,
			Remove:    true,
		}
		p.Peers = append(p.Peers, update)
	}
	data, err := json.Marshal(p)
	if err != nil {
		logger.Log(0, "marshal peer update", err.Error())
		return
	}
	publish(&client.Host, fmt.Sprintf("peer/host/%s/%s", client.Host.ID.String(), servercfg.GetServer()), data)
	//update the relay peer
	p = models.PeerAction{
		Action: models.UpdatePeer,
	}
	update := wgtypes.PeerConfig{
		PublicKey:         relay.Host.PublicKey,
		ReplaceAllowedIPs: true,
		Endpoint: &net.UDPAddr{
			IP:   relay.Host.EndpointIP,
			Port: relay.Host.ListenPort,
		},
		PersistentKeepaliveInterval: &relay.Node.PersistentKeepalive,
	}
	if relay.Node.Address.IP != nil {
		relay.Node.Address.Mask = net.CIDRMask(32, 32)
		update.AllowedIPs = append(update.AllowedIPs, relay.Node.Address)
	}
	if relay.Node.Address6.IP != nil {
		relay.Node.Address6.Mask = net.CIDRMask(128, 128)
		update.AllowedIPs = append(update.AllowedIPs, relay.Node.Address6)
	}
	p.Peers = append(p.Peers, update)
	// add all other peers to allowed ips
	for _, peer := range peers {
		if peer.Host.ID == relay.Host.ID || peer.Host.ID == client.Host.ID {
			continue
		}
		if nodeacls.AreNodesAllowed(nodeacls.NetworkID(client.Node.Network), nodeacls.NodeID(client.Node.ID.String()), nodeacls.NodeID(peer.Node.ID.String())) {
			update.AllowedIPs = append(update.AllowedIPs, logic.AddAllowedIPs(&peer)...)
		}
	}
	p.Peers = append(p.Peers, update)
	data, err = json.Marshal(p)
	if err != nil {
		logger.Log(0, "marshal peer update", err.Error())
		return
	}
	publish(&client.Host, fmt.Sprintf("peer/host/%s/%s", client.Host.ID.String(), servercfg.GetServer()), data)
}

// pubRelayUpdate - publish peer update to a relay
func pubRelayUpdate(client *models.Client, peers []models.Client) {
	if !client.Node.IsRelay {
		return
	}
	// add all peers to allowedips
	p := models.PeerAction{
		Action: models.UpdatePeer,
	}
	for _, peer := range peers {
		if peer.Host.ID == client.Host.ID {
			continue
		}
		update := wgtypes.PeerConfig{
			PublicKey:         peer.Host.PublicKey,
			ReplaceAllowedIPs: true,
			Remove:            false,
			Endpoint: &net.UDPAddr{
				IP:   peer.Host.EndpointIP,
				Port: peer.Host.ListenPort,
			},
			PersistentKeepaliveInterval: &peer.Node.PersistentKeepalive,
		}
		update.AllowedIPs = append(update.AllowedIPs, logic.AddAllowedIPs(&peer)...)
		p.Peers = append(p.Peers, update)
	}
	data, err := json.Marshal(p)
	if err != nil {
		logger.Log(0, "marshal peer update", err.Error())
		return
	}
	publish(&client.Host, fmt.Sprintf("peer/host/%s/%s", client.Host.ID.String(), servercfg.GetServer()), data)
}

func BroadCastRelayUpdate(relayReq models.RelayRequest) error {
	/* TODO:
	1. FlushPeersTo Relayed Node
	2. BroadCast Remove relayed Peer on network peer
	3. BroadCast Update Relay peer on netmaker peer
	*/
	clients, err := logic.GetNetworkClients(relayReq.NetID)
	if err != nil {
		return err
	}
	// filter relay Node
	filteredClients := clients
	for i := len(filteredClients) - 1; i >= 0; i-- {
		if filteredClients[i].Node.ID.String() == relayReq.NodeID {
			filteredClients = append(filteredClients[:i], filteredClients[i+1:]...)
			break
		}
	}
	for _, relayedNodeID := range relayReq.RelayedNodes {
		relayedNode, err := logic.GetNodeByID(relayedNodeID)
		if err != nil {
			continue
		}

		h, err := logic.GetHost(relayedNode.HostID.String())
		if err != nil {
			continue
		}
		BroadcastDelPeer(h, filteredClients)
		FlushNetworkPeersToHost(&models.Client{Host: *h, Node: relayedNode}, clients)
	}
	relayNode, err := logic.GetNodeByID(relayReq.NodeID)
	if err != nil {
		return err
	}
	relayHost, err := logic.GetHost(relayNode.HostID.String())
	if err != nil {
		return err
	}

	return BroadcastAddOrUpdateNetworkPeer(&models.Client{Host: *relayHost, Node: relayNode}, true)
}

func BroadCastRelayRemoval(network string) error {
	clients, err := logic.GetNetworkClients(network)
	if err != nil {
		return err
	}
	for _, client := range clients {
		client := client
		go FlushNetworkPeersToHost(&client, clients)
	}
	return err
}