package observe

import (
	"fmt"
	"math/rand"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/vshn/k8up/log"
	"github.com/vshn/k8up/service"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	jobRunning = "running"
	jobDeleted = "deleted"
)

var (
	metricsFailureCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "k8up_jobs_failed_counter",
		Help: "The total number of backups that failed",
	}, promLabels)
	metricsSuccessCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "k8up_jobs_successful_counter",
		Help: "The total number of backups that went through cleanly",
	}, promLabels)
	metricsTotalCounter = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "k8up_jobs_total",
		Help: "The total amount of all jobs run",
	}, promLabels)

	promLabels = []string{
		"namespace",
		"jobType",
	}
)

type topic string

// PodState contains the state of a pod as well as meta information for the
// subscription system.
type PodState struct {
	BaasID     string
	State      batchv1.JobConditionType
	Repository string
}

// Broker holds the subscribers per topic. So that every subscriber for each
// topic can be notified at a time. The topic is a random UUID each baas
// resource gets assigned during creation.
type Broker struct {
	subscribers map[topic][]Subscriber
	mutex       sync.Mutex
}

// Subscriber holds a channel that will receive the updates. The id is for
// internal tracking.
type Subscriber struct {
	CH        chan PodState
	id        int    // ID has to be uniqe within a topic
	TopicName string // contains the name that this subscriber is registered with
}

// WatchObjects contains everything needed to watch jobs. It can also hold
// functions that get triggered during the equivalent event (success,fail,running)
type WatchObjects struct {
	Logger      log.Logger
	Job         *batchv1.Job
	Locker      Locker
	jobType     JobType
	JobName     JobName
	Successfunc func(message PodState)
	Failedfunc  func(message PodState)
	Runningfunc func(message PodState)
	Defaultfunc func(message PodState)
	K8sCli      kubernetes.Interface
}

// update sends an update to a single subscriber
func (s *Subscriber) update(state PodState) {
	s.CH <- state
}

func newBroker() *Broker {
	return &Broker{
		subscribers: make(map[topic][]Subscriber, 0),
		mutex:       sync.Mutex{},
	}
}

// Subscribe adds a subscriber to the broker under the correct topic and returns
// the subscriber. The subscriber contains the means to listen to events if necessary.
func (b *Broker) Subscribe(topicName string) (*Subscriber, error) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if subs, ok := b.subscribers[topic(topicName)]; !ok {
		tmpSlice := make([]Subscriber, 0)

		tmpSub := Subscriber{
			CH:        make(chan PodState, 0),
			id:        rand.Int(),
			TopicName: topicName,
		}

		tmpSlice = append(tmpSlice, tmpSub)

		b.subscribers[topic(topicName)] = tmpSlice

		return &tmpSub, nil

	} else {
		exists := true
		for exists {
			newID := rand.Int()
			exists = false
			for i := range subs {
				if subs[i].id == newID {
					exists = true
					break
				}
			}
			if !exists {
				tmpSub := Subscriber{
					id: newID,
					CH: make(chan PodState, 0),
				}
				subs = append(subs, tmpSub)
				b.subscribers[topic(topicName)] = subs
				return &tmpSub, nil
			}
		}
		return nil, fmt.Errorf("Could not register")
	}
}

// Unsubscribe removes the provided subscriber from the topic.
func (b *Broker) Unsubscribe(topicName string, subscriber *Subscriber) {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if subs, ok := b.subscribers[topic(topicName)]; ok {
		deleteIndex := 0
		for i := range subs {
			if subs[i].id == subscriber.id {
				deleteIndex = i
			}
		}
		close(subs[deleteIndex].CH)
		b.subscribers[topic(topicName)] = append(subs[:deleteIndex], subs[deleteIndex+1:]...)
	}
}

// Notify notifies all subscribers to topic with the state. If it wants to
// notify a topic that doesn't exist it will return an error. The most likely
// cause for this is if the operator is restarted and there are still pods
// around. In that case it can be safely ignored. It's planned, that the
// operator should also register jobs that aren't created by the same, for cases
// where the operator gets evicted or HA setups.
func (b *Broker) Notify(topicName string, state PodState) error {
	b.mutex.Lock()
	defer b.mutex.Unlock()
	if subs, ok := b.subscribers[topic(topicName)]; ok {
		for i := range subs {
			go subs[i].update(state)
		}
	} else {
		if topicName == "" {
			return nil
		}
		return fmt.Errorf("%v is not a registered topic", topicName)
	}
	return nil
}

// WatchLoop loops over the channel. It will run the WatchObject functions when
// the appriopriate state is triggered (running, success, fail). This way each
// service can provide custom code that should get executed on the state changes
// if necessary.
func (s *Subscriber) WatchLoop(watch WatchObjects) {

	running := false
	backendString := service.GetRepository(&corev1.Pod{Spec: watch.Job.Spec.Template.Spec})

	jobString := fmt.Sprintf("%v/%v", watch.Job.GetNamespace(), watch.Job.GetName())

	defer GetInstance().GetBroker().Unsubscribe(s.TopicName, s)

	// initialise the metrics, as there's no rate change if it just pops up with
	// the value of 1.
	initMetrics(watch.Job.GetNamespace(), string(watch.JobName))

	for message := range s.CH {
		switch message.State {
		case batchv1.JobFailed:
			watch.Logger.Errorf("%v failed", jobString)
			if watch.Failedfunc != nil {
				watch.Failedfunc(message)
			}
			watch.Locker.Decrement(watch.jobType)
			incrFailure(watch.Job.GetNamespace(), string(watch.jobType.Name))
			return
		case batchv1.JobComplete:
			watch.Logger.Infof("%v finished successfully", jobString)
			if watch.Successfunc != nil {
				watch.Successfunc(message)
			}
			watch.Locker.Decrement(watch.jobType)
			incSuccess(watch.Job.GetNamespace(), string(watch.jobType.Name))
			return
		default:
			watch.Logger.Infof("%v is %v", jobString, jobRunning)
			if !running {
				running = true
				watch.jobType = watch.Locker.Increment(backendString, watch.JobName)
			}
		}
	}
}

func incrFailure(namespace, jobType string) {
	metricsFailureCounter.WithLabelValues(namespace, jobType).Inc()
	metricsTotalCounter.WithLabelValues(namespace, jobType).Inc()
}

func incSuccess(namespace, jobType string) {
	metricsSuccessCounter.WithLabelValues(namespace, jobType).Inc()
	metricsTotalCounter.WithLabelValues(namespace, jobType).Inc()
}

func initMetrics(namespace, jobType string) {
	metricsFailureCounter.WithLabelValues(namespace, jobType).Add(0)
	metricsSuccessCounter.WithLabelValues(namespace, jobType).Add(0)
}
