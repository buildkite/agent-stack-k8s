package monitor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Khan/genqlient/graphql"
	"github.com/buildkite/agent-stack-k8s/api"
	lru "github.com/hashicorp/golang-lru/v2"
	"go.uber.org/zap"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/selection"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
)

type Monitor struct {
	ctx         context.Context
	gql         graphql.Client
	k8s         kubernetes.Interface
	logger      *zap.Logger
	knownBuilds *lru.Cache[string, struct{}]
	cfg         Config
	jobs        chan Job
	once        sync.Once
}

type Config struct {
	Namespace   string
	Token       string
	MaxInFlight int
	Org         string
	Tags        []string
}

type Job struct {
	api.CommandJob
	Err error
	Tag string
}

func New(ctx context.Context, logger *zap.Logger, k8sClient kubernetes.Interface, cfg Config) (*Monitor, error) {
	graphqlClient := api.NewClient(cfg.Token)
	length := cfg.MaxInFlight * 10
	if cfg.MaxInFlight == 0 {
		// there are other protections for
		// ensuring no duplicate jobs
		// this length just is an early-stage protection against duplicate
		// jobs in flight
		length = 1000
	}
	cache, err := lru.New[string, struct{}](length)
	if err != nil {
		return nil, err
	}
	m := &Monitor{
		ctx:         ctx,
		gql:         graphqlClient,
		k8s:         k8sClient,
		logger:      logger,
		knownBuilds: cache,
		cfg:         cfg,
		jobs:        make(chan Job),
	}
	if err := m.synchronize(ctx); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *Monitor) Scheduled() <-chan Job {
	go m.once.Do(func() { go m.start() })
	return m.jobs
}

func (m *Monitor) start() {
	m.logger.Debug("started", zap.Strings("tags", m.cfg.Tags))
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if m.reachedMaxInFlight() {
				m.logger.Debug("max in flight reached", zap.Int("in-flight", m.cfg.MaxInFlight))
				continue
			}
		Out:
			for _, tag := range m.cfg.Tags {
				buildsResponse, err := api.GetScheduledBuilds(m.ctx, m.gql, m.cfg.Org, []string{tag})
				if err != nil {
					if errors.Is(err, context.Canceled) {
						continue
					}
					m.logger.Warn("failed to retrieve builds for pipeline", zap.Error(err))
					continue
				}
				if buildsResponse.Organization.Id == nil {
					m.jobs <- Job{Err: fmt.Errorf("invalid organization: %s", m.cfg.Org)}
				}
				builds := buildsResponse.Organization.Jobs.Edges
				sort.Slice(builds, func(i, j int) bool {
					cmdI := builds[i].Node.(*api.JobJobTypeCommand)
					cmdJ := builds[j].Node.(*api.JobJobTypeCommand)

					return cmdI.ScheduledAt.Before(cmdJ.ScheduledAt)

				})

				for _, job := range builds {
					cmdJob := job.Node.(*api.JobJobTypeCommand)
					if m.knownBuilds.Contains(cmdJob.Uuid) {
						m.logger.Debug("skipping already queued job", zap.String("uuid", cmdJob.Uuid))
					} else if m.reachedMaxInFlight() {
						m.logger.Debug("max in flight reached", zap.Int("in-flight", m.cfg.MaxInFlight))
						break Out
					} else {
						m.logger.Debug("adding job", zap.String("uuid", cmdJob.Uuid))
						m.jobs <- Job{
							CommandJob: cmdJob.CommandJob,
							Tag:        tag,
						}
						m.logger.Debug("added job", zap.String("uuid", cmdJob.Uuid))
						m.knownBuilds.Add(cmdJob.Uuid, struct{}{})
					}
				}
			}
		}
	}
}

func (m *Monitor) synchronize(ctx context.Context) error {
	hasTag, err := labels.NewRequirement(api.TagLabel, selection.In, tagsToLabels(m.cfg.Tags))
	if err != nil {
		return fmt.Errorf("failed to create tag label requirement: %w", err)
	}
	hasUuid, err := labels.NewRequirement(api.UUIDLabel, selection.Exists, nil)
	if err != nil {
		return fmt.Errorf("failed to create uuid label requirement: %w", err)
	}
	selector := labels.NewSelector().Add(*hasTag, *hasUuid).String()
	lw := &cache.ListWatch{
		ListFunc: func(options metav1.ListOptions) (runtime.Object, error) {
			options.LabelSelector = selector
			return m.k8s.BatchV1().Jobs(m.cfg.Namespace).List(ctx, options)
		},
		WatchFunc: func(options metav1.ListOptions) (watch.Interface, error) {
			options.LabelSelector = selector
			return m.k8s.BatchV1().Jobs(m.cfg.Namespace).Watch(ctx, options)
		},
	}
	removeIfCompleted := func(obj interface{}) {
		job := obj.(*batchv1.Job)
		if isComplete(job) {
			uuid := job.Labels[api.UUIDLabel]
			m.logger.Debug("job finished", zap.String("uuid", uuid))
			m.knownBuilds.Remove(job.Labels[api.UUIDLabel])
		}
	}

	_, controller := cache.NewInformer(lw, &batchv1.Job{}, 0, cache.ResourceEventHandlerFuncs{
		AddFunc: func(obj interface{}) {
			job := obj.(*batchv1.Job)
			if !isComplete(job) {
				uuid := job.Labels[api.UUIDLabel]
				found, _ := m.knownBuilds.ContainsOrAdd(job.Labels[api.UUIDLabel], struct{}{})
				if !found {
					m.logger.Debug("adding previously scheduled job", zap.String("uuid", uuid))
				}
			}
		},
		UpdateFunc: func(_, newObj interface{}) {
			removeIfCompleted(newObj)
		},
		DeleteFunc: removeIfCompleted,
	})

	go controller.Run(ctx.Done())
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
			if controller.HasSynced() {
				m.logger.Debug("controller synchronized")
				return nil
			}
			m.logger.Debug("controller synchronizing")
		}
	}
}

func isComplete(job *batchv1.Job) bool {
	for _, condition := range job.Status.Conditions {
		if condition.Status == corev1.ConditionTrue {
			if condition.Type == batchv1.JobComplete || condition.Type == batchv1.JobFailed {
				return true
			}
		}
	}
	return false
}

func (m *Monitor) reachedMaxInFlight() bool {
	if m.cfg.MaxInFlight == 0 {
		return false
	}
	inFlight := m.knownBuilds.Len()
	return inFlight >= m.cfg.MaxInFlight
}

// a valid label must be an empty string or consist of alphanumeric characters,
// '-', '_' or '.', and must start and end with an alphanumeric character (e.g.
// 'MyValue',  or 'my_value',  or '12345', regex used for validation is
// '(([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9])?')
func TagToLabel(tag string) string {
	return strings.ReplaceAll(tag, "=", "_")
}

func tagsToLabels(tags []string) []string {
	labels := make([]string, len(tags))
	for i, tag := range tags {
		labels[i] = TagToLabel(tag)
	}
	return labels
}
