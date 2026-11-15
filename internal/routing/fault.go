package routing

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/nihat1337/urban-crisis-evacuation/internal/domain"
)

// faultRouter stands in for the external providers when an outage is being
// simulated (jury demo / chaos testing). Mode "503" fails instantly like an
// HTTP 503; mode "timeout" hangs for Delay (the configured routing timeout)
// before failing, exactly like a provider that never answers. It carries its
// own breaker so, like a real provider, it is skipped quickly after 3 failures.
type faultRouter struct {
	Mode  string
	Delay time.Duration
	breaker
}

func newFaultRouter(mode string, delay time.Duration) *faultRouter {
	return &faultRouter{Mode: mode, Delay: delay, breaker: newBreaker(3, 60*time.Second)}
}

func (f *faultRouter) Name() string { return "chaos:" + f.Mode }

func (f *faultRouter) Route(ctx context.Context, _, _ domain.Point, _ []domain.Road, _ Options) (domain.Route, error) {
	if ok, _ := f.healthy(); !ok {
		return domain.Route{}, fmt.Errorf("%w: circuit breaker open (simulated outage)", ErrUnavailable)
	}
	var err error
	switch f.Mode {
	case "timeout":
		select {
		case <-ctx.Done():
			err = errors.New("simulated timeout: context cancelled")
		case <-time.After(f.Delay):
			err = fmt.Errorf("simulated timeout after %s (no answer from routing API)", f.Delay)
		}
	default:
		err = errors.New("simulated HTTP 503 Service Unavailable from routing API")
	}
	f.fail(err)
	return domain.Route{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
}
