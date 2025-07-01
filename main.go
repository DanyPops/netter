package main

import (
	"fmt"
	"net"
	"os"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	current "github.com/containernetworking/cni/pkg/types/100"
	"github.com/containernetworking/cni/pkg/version"
	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ipam"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/vishvan/netlink"
)

// NetConf defines the network configuration that can be passed in the CNI config file.
type NetConf struct {
	types.NetConf
	Bridge string `json:"bridge"` // Name of the bridge on the host
}

// cmdAdd is called by Kubernetes when a pod is created.
func cmdAdd(args *skel.CmdArgs) error {
	// 1. Get network namespace and container interface name from arguments.
	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", args.Netns, err)
	}
	defer netns.Close()

	// 2. Create a veth pair. One end goes in the container, one stays on the host.
	hostIfaceName := fmt.Sprintf("veth%s", args.ContainerID[:8])
	hostIface := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{Name: hostIfaceName, MTU: 1500},
		PeerName:  args.IfName,
	}
	if err := netlink.LinkAdd(hostIface); err != nil {
		return fmt.Errorf("failed to create veth pair: %v", err)
	}

	// 3. Move the container-side of the veth into the container's network namespace.
	peer, err := netlink.LinkByName(args.IfName)
	if err != nil {
		return fmt.Errorf("failed to find peer interface %s: %v", args.IfName, err)
	}
	if err := netlink.LinkSetNsFd(peer, int(netns.Fd())); err != nil {
		return fmt.Errorf("failed to move peer to netns: %v", err)
	}

	// 4. Run the CNI's built-in IPAM plugin to get an IP for the pod.
	r, err := ipam.ExecAdd("host-local", args.StdinData)
	if err != nil {
		return err
	}
	result, err := current.GetResult(r)
	if err != nil {
		return err
	}

	// 5. Configure the pod's network namespace.
	err = netns.Do(func(_ ns.NetNS) error {
		// Set the pod's interface UP.
		if err := ip.EnableIP4Forwarding(); err != nil {
			return fmt.Errorf("failed to enable forwarding: %v", err)
		}
		link, err := netlink.LinkByName(args.IfName)
		if err != nil {
			return err
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return err
		}
		// Add the IP address to the pod's interface.
		addr := &netlink.Addr{IPNet: &result.IPs[0].Address}
		if err := netlink.LinkAddAddr(link, addr); err != nil {
			return err
		}
		// Add a default route.
		gw := result.IPs[0].Gateway
		_, defaultNet, _ := net.ParseCIDR("0.0.0.0/0")
		if err := ip.AddRoute(defaultNet, gw, link); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	// 6. Print the result (IP address, etc.) to stdout for Kubernetes to consume.
	return types.PrintResult(result, result.Version())
}

// cmdDel is called when a pod is deleted. We use the built-in IPAM to release the IP.
func cmdDel(args *skel.CmdArgs) error {
	if err := ipam.ExecDel("host-local", args.StdinData); err != nil {
		return err
	}
	return nil
}

func main() {
	// Use the skel library to drive the CNI plugin.
	skel.PluginMain(cmdAdd, nil, cmdDel, version.All, "netter-cni")
}
