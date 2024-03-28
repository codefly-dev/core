package network

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"net"

	"github.com/codefly-dev/core/configurations/standards"
)

type FixedStrategy struct {
}

func HashInt(s string, low, high int) int {
	hasher := sha256.New()
	hasher.Write([]byte(s))
	hash := hasher.Sum(nil)
	num := binary.BigEndian.Uint32(hash)
	return int(num%uint32(high-low)) + low
}

func APIInt(api string) int {
	switch api {
	case standards.TCP:
		return 0
	case standards.HTTP:
		return 1
	case standards.REST:
		return 2
	case standards.GRPC:
		return 3
	default:
		return 0
	}
}

// ToNamedPort strategy:
// APP-SVC-API
// Between 1100(0) and 4999(9)
// First 11 -> 49: hash app
// Next 0 -> 9: hash svc
// Next 0 - 9: hash name
// Last Digit: API
// 0: TCP
// 1: HTTP/ REST
// 2: gRPC
func ToNamedPort(_ context.Context, app string, svc string, name string, api string) uint16 {
	appPart := HashInt(app, 11, 49) * 1000
	svcPart := HashInt(app+svc, 0, 9) * 100
	namePart := HashInt(name, 0, 9) * 10
	port := appPart + svcPart + namePart + APIInt(api)
	return uint16(port)
}

func IsPortAvailable(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

//
//func killProcessUsingPort(port int) error {
//	pid, err := getPidUsingPort(port)
//	if err != nil {
//		return err
//	}
//	if pid != "" {
//		return killProcess(pid)
//	}
//	return nil
//}

//
//func getPidUsingPort(port int) (string, error) {
//	cmd := exec.Command("lsof", "-n", fmt.Sprintf("-i4TCP:%d", port))
//	output, err := cmd.CombinedOutput()
//	if err != nil {
//		return "", err
//	}
//	lines := strings.Split(string(output), "\n")
//	if len(lines) > 1 {
//		fields := strings.Fields(lines[1])
//		if len(fields) >= 2 {
//			return fields[1], nil // PID is the second field
//		}
//	}
//	return "", nil
//}

//func killProcess(pid string) error {
//	pidInt, _ := strconv.Atoi(pid)
//	process, err := os.FindProcess(pidInt)
//	if err != nil {
//		return err
//	}
//	err = process.Kill()
//	if err != nil {
//		return err
//	}
//	return nil
//}

//
//func (r FixedStrategy) Reserve(ctx context.Context, host string, mappings []*ApplicationMapping) (*ApplicationEndpointInstances, error) {
//	w := wool.Get(ctx).In("FixedStrategy.Reserve")
//	m := &ApplicationEndpointInstances{}
//	for _, mapping := range mappings {
//		api, err := configurations.APIAsStandard(mapping.Endpoint.Api)
//		if err != nil {
//			return nil, w.Wrapf(err, "cannot get api")
//		}
//		port := ToNamedPort(ctx, mapping.Endpoint.Application, mapping.Endpoint.Service, mapping.Endpoint.Name, api)
//		w.Debug("reserving", wool.ApplicationField(mapping.Endpoint.Application), wool.ServiceField(mapping.Endpoint.Service), wool.Field("port", port))
//		w.Trace("port", wool.ThisField(mapping), wool.Field("port", port))
//		m.ApplicationMappingInstances = append(m.ApplicationMappingInstances,
//			&ApplicationEndpointInstance{
//				ApplicationMapping: mapping,
//				Port:               port,
//				Host:               host,
//			})
//	}
//	return m, nil
//}
//
//// NewServicePortManager manages the ports for a service
//func NewServicePortManager(_ context.Context) (*ServiceManager, error) {
//	return &ServiceManager{
//		strategy: &FixedStrategy{},
//		ids:      make(map[string]int),
//		host:     "localhost",
//	}, nil
//}
