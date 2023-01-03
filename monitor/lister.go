package monitor

import (
	"context"
	"fmt"
	"log"

	"github.com/buildkite/agent-stack-k8s/api"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	batchv1 "k8s.io/client-go/listers/batch/v1"
	"k8s.io/client-go/tools/cache"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
)

func NewJobListerOrDie(ctx context.Context, clientset kubernetes.Interface, tags ...string) batchv1.JobLister {
	hasTag, err := labels.NewRequirement(api.TagLabel, selection.In, api.TagsToLabels(tags))
	if err != nil {
		log.Panic("Failed to build tag label selector for job manager", err)
	}
	hasUuid, err := labels.NewRequirement(api.UUIDLabel, selection.Exists, nil)
	if err != nil {
		log.Panic("Failed to build uuid label selector for job manager", err)
	}
	factory := informers.NewSharedInformerFactoryWithOptions(clientset, 0, informers.WithTweakListOptions(func(opt *metav1.ListOptions) {
		opt.LabelSelector = labels.NewSelector().Add(*hasTag, *hasUuid).String()
	}))
	informer := factory.Batch().V1().Jobs()
	jobInformer := informer.Informer()
	indexer := cache.NewIndexer(MetaJobLabelKeyFunc, cache.Indexers{cache.NamespaceIndex: cache.MetaNamespaceIndexFunc})
	jobInformer.AddIndexers(indexer.GetIndexers())

	go factory.Start(ctx.Done())

	if !cache.WaitForCacheSync(ctx.Done(), jobInformer.HasSynced) {
		log.Panic(fmt.Errorf("Failed to sync informer cache"))
	}

	return informer.Lister()
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
	if v, ok := labels[api.UUIDLabel]; ok {
		return v, nil
	}

	return meta.GetName(), nil
}
