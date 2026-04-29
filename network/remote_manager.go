package network

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/standards"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type RemoteManager struct {
	dnsManager DNSManager

	// pairingsWG tracks the goroutines spawned by StartPairing
	// (port-forward + log fetch). Stop() blocks on it so callers can
	// guarantee the goroutines (and their kubectl child processes) are
	// torn down before they continue. Without this the goroutines were
	// fire-and-forget and outlived the calling context.
	pairingsWG sync.WaitGroup
}

// Stop blocks until every pairing goroutine has exited. The caller is
// expected to have cancelled the context passed to StartPairing first
// (or all pairing contexts are children of one that just got cancelled).
// Safe to call multiple times.
func (m *RemoteManager) Stop() {
	m.pairingsWG.Wait()
}

func (m *RemoteManager) GetNamespace(_ context.Context, env *resources.Environment, workspace *resources.Workspace, service *resources.ServiceIdentity) (string, error) {
	if workspace.Layout == resources.LayoutKindFlat {
		return fmt.Sprintf("%s-%s", workspace.Name, env.Name), nil
	}
	return fmt.Sprintf("%s-%s-%s", workspace.Name, service.Module, env.Name), nil
}

func (m *RemoteManager) KubernetesService(service *resources.ServiceIdentity, endpoint *basev0.Endpoint, namespace string, port uint16) *basev0.NetworkInstance {
	host := fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, namespace)
	var instance *basev0.NetworkInstance
	if endpoint.Api == standards.HTTP || endpoint.Api == standards.REST {
		instance = resources.NewHTTPNetworkInstance(host, port, false)
	} else {
		instance = resources.NewNetworkInstance(host, port)
	}
	instance.Access = resources.NewContainerNetworkAccess()
	return instance
}

// GenerateNetworkMappings generates network mappings for a service endpoints
func (m *RemoteManager) GenerateNetworkMappings(ctx context.Context,
	env *resources.Environment,
	workspace *resources.Workspace,
	service *resources.ServiceIdentity,
	endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error) {
	w := wool.Get(ctx).In("network.Runtime.GenerateNetworkMappings")
	if m.dnsManager == nil {
		return nil, w.NewError("RemoteManager: dnsManager is nil — call NewRemoteManager with a non-nil DNSManager")
	}
	var out []*basev0.NetworkMapping
	for _, endpoint := range endpoints {
		nm := &basev0.NetworkMapping{
			Endpoint: endpoint,
		}
		// External endpoints (e.g. public load-balanced) require a
		// declared DNS — there's no sane fallback because the public
		// hostname is environment-specific (a wildcard cert,
		// CNAME, etc.). Hard-fail when missing.
		if endpoint.Visibility == resources.VisibilityExternal {
			dns, err := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
			if err != nil {
				return nil, err
			}
			if dns == nil {
				return nil, w.NewError("cannot find dns for endpoint %s", endpoint.Name)
			}
			nm.Instances = []*basev0.NetworkInstance{
				ExternalInstance(DNS(service, endpoint, dns)),
			}
			out = append(out, nm)
			continue
		}

		// Internal endpoints. Lookup order:
		//
		//   1. Declared DNS (a dns/<env>/dns.codefly.yaml entry — the
		//      historical override path used to point services at
		//      host.docker.internal during `codefly run`).
		//
		//   2. Cluster-internal DNS synthesized from the service's
		//      Kubernetes Service name + namespace. This is what
		//      "modern deploy" wants by default — an in-cluster app
		//      should reach `<svc>.<ns>.svc.cluster.local` regardless
		//      of any user-authored YAML.
		//
		//   3. Localhost fallback for `local*` environments without a
		//      declared DNS — preserves the legacy `codefly run`
		//      behavior on dev machines.
		port := standards.Port(endpoint.Api)
		dns, dnsErr := m.dnsManager.GetDNS(ctx, service, endpoint.Name)
		if dnsErr != nil && !env.Local() {
			// Cluster envs: synthesize cluster-internal DNS so deploys
			// don't depend on a user-authored dns.codefly.yaml file.
			// Resolves issue #56 — saas-starter deploys hit "no DNS
			// found" because their dns/ dirs are tuned for run, not
			// for in-cluster k8s.
			namespace, nsErr := m.GetNamespace(ctx, env, workspace, service)
			if nsErr != nil {
				return nil, nsErr
			}
			dns = &basev0.DNS{
				Name:     service.Unique(),
				Module:   service.Module,
				Service:  service.Name,
				Endpoint: endpoint.Name,
				Host:     fmt.Sprintf("%s.%s.svc.cluster.local", service.Name, namespace),
				Port:     uint32(port),
				Secured:  false,
			}
		} else if dnsErr != nil {
			// local-env fallback: bind to localhost on the canonical
			// port for the API kind.
			dns = &basev0.DNS{
				Name:     service.Unique(),
				Module:   service.Module,
				Service:  service.Name,
				Endpoint: endpoint.Name,
				Host:     "localhost",
				Port:     uint32(port),
				Secured:  false,
			}
		}
		if dns != nil {
			nm.Instances = []*basev0.NetworkInstance{
				PublicInstance(DNS(service, endpoint, dns)),
			}
			w.Debug("will expose public endpoint to load balancer", wool.Field("dns", dns))
		}

		namespace, err := m.GetNamespace(ctx, env, workspace, service)
		if err != nil {
			return nil, err
		}
		nm.Instances = append(nm.Instances, ContainerInstance(m.KubernetesService(service, endpoint, namespace, port)))
		out = append(out, nm)
	}
	return out, nil
}

type Pairing struct {
	Local  *basev0.NetworkMapping
	Remote *basev0.NetworkMapping
}

func (m *RemoteManager) Expose(ctx context.Context,
	env *resources.Environment,
	workspace *resources.Workspace,
	service *resources.ServiceIdentity,
	endpoints []*basev0.Endpoint,
	localNetworkMappings []*basev0.NetworkMapping,
	output wool.LogProcessorWithSource) error {
	w := wool.Get(ctx).In("expose")
	remotes, err := m.GenerateNetworkMappings(ctx, env, workspace, service, endpoints)
	if err != nil {
		return w.Wrapf(err, "can't generate remote mappings")
	}
	var pairings []*Pairing
	for _, mapping := range localNetworkMappings {
		// Find the equivalent remote network mapping
		var remoteMapping *basev0.NetworkMapping
		for _, r := range remotes {
			if r.Endpoint.Module == mapping.Endpoint.Module &&
				r.Endpoint.Service == mapping.Endpoint.Service &&
				r.Endpoint.Name == mapping.Endpoint.Name &&
				r.Endpoint.Api == mapping.Endpoint.Api {
				remoteMapping = r
				break
			}
		}
		if remoteMapping == nil {
			return w.NewError("cannot find remote network mapping for local mapping")
		}
		pairings = append(pairings, &Pairing{Local: mapping, Remote: remoteMapping})
	}
	for _, pairing := range pairings {
		err = m.StartPairing(ctx, env, workspace, service, pairing, output)
		if err != nil {
			return w.Wrap(err)
		}
	}
	return nil
}

func (m *RemoteManager) StartPairing(ctx context.Context, _ *resources.Environment, _ *resources.Workspace, service *resources.ServiceIdentity, pairing *Pairing, output wool.LogProcessorWithSource) error {
	w := wool.Get(ctx).In("startPairing")
	// Get the remote service
	if len(pairing.Remote.Instances) != 1 {
		return w.NewError("remote service must have exactly one instance")
	}
	remote := pairing.Remote.Instances[0]
	remoteService, err := m.GetKubernetesService(ctx, service, remote.Hostname, uint16(remote.Port))
	if err != nil {
		return w.Wrap(err)
	}
	// Find the native instances
	local := resources.FilterNetworkInstance(ctx, pairing.Local.Instances, resources.NewNativeNetworkAccess())
	if local == nil {
		return w.NewError("no native instance found in local network mapping")
	}
	// Each goroutine gets its own err binding — the previous version
	// shared the outer `err` between two parallel writers, which is a
	// data race. The WaitGroup lets Stop() block until both kubectl
	// child processes are reaped.
	m.pairingsWG.Add(2)
	go func() {
		defer m.pairingsWG.Done()
		if err := portForwardService(ctx, remoteService, uint16(local.Port)); err != nil {
			w.Warn(err.Error())
		}
	}()
	go func() {
		defer m.pairingsWG.Done()
		if err := fetchLogs(ctx, remoteService, output); err != nil {
			w.Warn(err.Error())
		}
	}()

	return nil
}

func (m *RemoteManager) GetKubernetesService(ctx context.Context, identity *resources.ServiceIdentity, hostname string, port uint16) (*KubernetesService, error) {
	w := wool.Get(ctx).In("getKubernetesService")
	// Parse: backend.codefly-platform-customers-local.svc.cluster.local

	hostParts := strings.Split(hostname, ".")
	if len(hostParts) < 3 {
		return nil, w.NewError("invalid host format: %s", hostname)
	}

	name := hostParts[0]
	namespace := hostParts[1]

	return &KubernetesService{
		Namespace:       namespace,
		Name:            name,
		Port:            port,
		ServiceIdentity: identity,
	}, nil
}

type KubernetesService struct {
	Namespace string
	Name      string
	Port      uint16
	*resources.ServiceIdentity
}

func portForwardService(ctx context.Context, k8sSvc *KubernetesService, localPort uint16) error {
	w := wool.Get(ctx).In("portForwardService")
	//nolint:gosec // G204: Subprocess launched with a potential tainted input or cmd arguments
	cmd := exec.CommandContext(ctx, "kubectl", "port-forward", "-n", k8sSvc.Namespace, fmt.Sprintf("svc/%s", k8sSvc.Name), fmt.Sprintf("%d:%d", localPort, k8sSvc.Port))
	w.Info("port-forward", wool.Field("cmd", cmd.Args))
	out, err := cmd.CombinedOutput()
	if err != nil {
		if strings.Contains(err.Error(), "signal: killed") {
			return nil
		}
		if strings.Contains(err.Error(), "context canceled") {
			return nil
		}
		return w.NewError("Failed to forward service: %s, %s, error: %v, out: %s", k8sSvc.Unique(), cmd.Args, err, out)
	}
	return nil
}

func fetchLogs(ctx context.Context, k8sService *KubernetesService, output wool.LogProcessorWithSource) error {
	w := wool.Get(ctx).In("fetchLogs").With(wool.Field("namespace", k8sService.Namespace), wool.ThisField(k8sService))
	identifier := &wool.Identifier{Unique: k8sService.Unique(), Kind: "SERVICE"}
	//nolint:gosec // G204: Subprocess launched with a potential tainted input or cmd arguments
	logsCmd := exec.CommandContext(ctx, "kubectl", "logs", "-n", k8sService.Namespace, fmt.Sprintf("svc/%s", k8sService.Name))
	w.Info("forwarding logs", wool.Field("cmd", logsCmd.Args))
	stdout, err := logsCmd.StdoutPipe()
	if err != nil {
		return w.Wrapf(err, "error creating StdoutPipe")
	}

	err = logsCmd.Start()
	if err != nil {
		return w.Wrapf(err, "error starting logs command for k8sService")
	}

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		text := scanner.Text()
		if strings.Contains(text, "failed to try resolving symlinks in path") {
			continue // Ignore this specific error message
		}
		output.ProcessWithSource(identifier, &wool.Log{Message: text, Level: wool.FORWARD})
	}

	if err = scanner.Err(); err != nil {
		return w.Wrapf(err, "error scanning logs for k8sService")
	}
	err = logsCmd.Wait()
	if err != nil {
		return w.Wrapf(err, "error waiting for logs command for k8sService")
	}
	return nil
}

func NewRemoteManager(_ context.Context, dnsManager DNSManager) (*RemoteManager, error) {
	return &RemoteManager{dnsManager: dnsManager}, nil
}
