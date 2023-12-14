package services

import (
	"sync"
)

type ServiceEvent struct {
	Unique string
	Event  string
}

type Tracker interface {
	Start(events chan<- ServiceEvent) error
	Stop()
	// Tracks() []*applications.Tracked
}

/*
First target tracker
*/

type SingleTracker struct {
	Tracked Tracked

	// latest
	// usage  *Usage
	// status ProcessState

	// internal
	// ctx    context.Action
	cancel func()
	sync.RWMutex
	stopping bool
}

func (t *SingleTracker) Stop() {
	t.Lock()
	defer t.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	t.stopping = true
}

//
//func NewSingleTracker(service *configurations.Agent, runtime services.Runtime, tracker *runtime.Tracker) (*SingleTracker, error) {
//	logger := shared.NewLogger().With("monitoring.NewSingleTracker<%s>", service.Name)
//	tracked, err := NewTracked(service, tracker)
//	if err != nil {
//		return nil, logger.Wrapf(err, "cannot create tracked")
//	}
//	ctx := context.Background()
//	ctx, cancel := context.WithCancel(ctx)
//	return &SingleTracker{Tracked: tracked, Runtime: runtime, ctx: ctx, cancel: cancel}, nil
//}
//
//func (t *SingleTracker) Start(events chan<- ServiceEvent) error {
//	logger := shared.NewLogger().With("monitoring.SingleTracker.Start")
//	ticker := time.NewTicker(1 * time.Second)
//	go func() {
//		for {
//			select {
//			case <-t.ctx.Done():
//				return
//			case <-ticker.C:
//				if t.Runtime == nil {
//					continue
//				}
//				t.RLock()
//				if t.stopping {
//					return
//				}
//				t.RUnlock()
//				req, err := t.Runtime.Information(&runtime.InformationRequest{})
//				if err != nil {
//					logger.Debugf("cannot get status from runtime: %v", err)
//					continue
//				}
//				if req.State == services.RestartWanted {
//					logger.Debugf("runtime wants to restart")
//					events <- ServiceEvent{
//						Unique: t.Tracked.Unique(),
//						Event:  "RestartWanted",
//					}
//					t.Lock()
//					t.stopping = true
//					t.Unlock()
//				}
//				if t.Tracked == nil {
//					return
//				}
//				status, err := t.Tracked.GetStatus()
//				if err == nil {
//					t.Lock()
//					t.status = status
//					t.Unlock()
//				}
//				usage, err := t.Tracked.GetUsage()
//				if err == nil {
//					t.Lock()
//					t.usage = usage
//					t.Unlock()
//				} else {
//					logger.TODO("cant get usage ")
//					t.Lock()
//					t.usage = &Usage{}
//					t.Unlock()
//				}
//			}
//		}
//	}()
//	return nil
//}
//
///*
//Multiple targets tracker
//*/
//
//type GroupTracker struct{}
//
////
////func (g GroupTracker) Tracks() []*applications.Tracked {
////	panic("implement me")
////}
//
//func (g GroupTracker) Start(events chan<- ServiceEvent) error {
//	// TODO implement me
//	panic("implement me")
//}
//
//func (g GroupTracker) Stop() {
//	// TODO implement me
//	panic("implement me")
//}
//
//func NewGroupTracker(service *configurations.Agent, runtime services.Runtime, trackers []*runtime.Tracker) (*GroupTracker, error) {
//	return &GroupTracker{}, nil
//}
//
///*
//Name tracker
//*/
//
//type ServiceTracker struct {
//	active map[string]Tracker
//	sync.RWMutex
//	events   chan<- ServiceEvent
//	trackers map[string]*runtime.TrackerList
//}
//
//func (t *ServiceTracker) OnHold(service *configurations.Agent, runtime services.Runtime) error {
//	logger := shared.NewLogger().With("monitoring.ServiceTracker.OnHold<%s>", service.Name)
//	tracker := &RestartTracker{unique: service.Unique(), runtime: runtime}
//	// Start errors first or start working in a non-blocking way
//	err := tracker.Start(t.events)
//	if err != nil {
//		return logger.Wrapf(err, "cannot start on-hold")
//	}
//	t.Lock()
//	t.active[service.Unique()] = tracker
//	t.Unlock()
//	return nil
//}
//
//func (t *ServiceTracker) Track(ctx context.Action, service *configurations.Agent, runtime services.Runtime, trackers []*runtime.Tracker) error {
//	logger := shared.NewLogger().With("monitoring.ServiceTracker.Track<%s>", service.Name)
//	tracker, err := CreateTracker(service, runtime, trackers)
//	if err != nil {
//		return logger.Wrapf(err, "cannot create tracker")
//	}
//	if tracker == nil {
//		return nil
//	}
//	// Start errors first or start working in a non-blocking way
//	err = tracker.Start(t.events)
//	if err != nil {
//		return logger.Wrapf(err, "cannot start tracker")
//	}
//	t.Lock()
//	t.trackers[service.Unique()] = &runtime.TrackerList{Trackers: trackers}
//	t.active[service.Unique()] = tracker
//	t.Unlock()
//	return nil
//}
//
//func (t *ServiceTracker) Untrack(service *configurations.Agent) error {
//	t.Lock()
//	defer t.Unlock()
//	unique := service.Unique()
//	if v, ok := t.active[unique]; ok {
//		v.Stop()
//	}
//	delete(t.active, unique)
//	delete(t.trackers, unique)
//
//	return nil
//}
//
////
////func (t *ServiceTracker) Tracks() []*applications.Tracked {
////	var tracks []*applications.Tracked
////	for _, tracker := range t.active {
////		tracks = append(tracks, tracker.Tracks()...)
////	}
////	return tracks
////}
//
//func CreateTracker(service *configurations.Agent, runtime services.Runtime, trackers []*runtime.Tracker) (Tracker, error) {
//	if len(trackers) == 0 {
//		return nil, nil
//	}
//	if len(trackers) == 1 {
//		return NewSingleTracker(service, runtime, trackers[0])
//	}
//	return NewGroupTracker(service, runtime, trackers)
//}
//
//func NewServiceTracker(events chan<- ServiceEvent) (*ServiceTracker, error) {
//	tracker := &ServiceTracker{
//		events:   events,
//		active:  make(map[string]Tracker),
//		trackers: make(map[string]*runtime.TrackerList),
//	}
//	return tracker, nil
//}
