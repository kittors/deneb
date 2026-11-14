package main

import "fmt"

// Keep distinct services distinct. When their public names are equal, the
// port is needed to tell which Open button and switch the user is choosing.
func serviceLabel(all []service, s service) string {
	for _, other := range all {
		if other.name == s.name && other.svc.Key != s.svc.Key {
			return fmt.Sprintf("%s · %s", s.name, s.svc.Ports)
		}
	}
	return s.name
}
