package api

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	batchv1api "k8s.io/api/batch/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	batchv1 "k8s.io/client-go/listers/batch/v1"
	"k8s.io/client-go/tools/cache"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

type BuildkiteJobManager struct {
	batchv1.JobLister
	kubernetes.Interface
	ActiveJobs *int32
	Tags       []string
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

func NewBuildkiteJobManager(ctx context.Context, clientset kubernetes.Interface, tags ...string) *BuildkiteJobManager {
	var activeJobs int32
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithTweakListOptions(func(opt *metav1.ListOptions) {
		hasTag, err := labels.NewRequirement(TagLabel, selection.In, tagsToLabels(tags))
		if err != nil {
			return
		}
		hasUuid, err := labels.NewRequirement(UUIDLabel, selection.Exists, nil)
		if err != nil {
			return
		}
		opt.LabelSelector = labels.NewSelector().Add(*hasTag, *hasUuid).String()
		opt.FieldSelector = "status.successful!=1"
	}))
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	indexer := cache.NewIndexer(MetaJobLabelKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	jobInformer.AddIndexers(indexer.GetIndexers())

	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
		//TODO handle error here
		log.Panic(fmt.Errorf("Failed to sync informer cache"))
	}

	jobInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc: func(_ interface{}) { atomic.AddInt32(&activeJobs, 1) },
		UpdateFunc: func(_ interface{}, obj interface{}) {
			job := obj.(batchv1api.Job)
			if job.Status.Active != 1 {
				atomic.AddInt32(&activeJobs, -1)
			}
		},
		DeleteFunc: func(obj interface{}) {
			job := obj.(batchv1api.Job)
			if job.Status.Active == 1 {
				atomic.AddInt32(&activeJobs, -1)
			}
		},
	})

	return &BuildkiteJobManager{
		JobLister:  informer.Lister(),
		Interface:  clientset,
		Tags:       tags,
		ActiveJobs: &activeJobs,
	}
}

func MetaJobLabelKeyFunc(obj interface{}) (string, error) {
	if key, ok := obj.(cache.ExplicitKey); ok {
		return string(key), nil
	}
	meta, err := meta.Accessor(obj)
	if err != nil {
		return "", fmt.Errorf("object has no meta: %v", err)
	}
	labels := meta.GetLabels()
	if v, ok := labels[UUIDLabel]; ok {
		return v, nil
	}

	return meta.GetName(), nil
}
