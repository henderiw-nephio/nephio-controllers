/*
Copyright 2023 The Nephio Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package bootstrap

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/henderiw-nephio/bootstrap-controller/pkg/applicator"
	"github.com/henderiw-nephio/nephio-controllers/pkg/meta"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/clientcmd"
	capiv1beta1 "sigs.k8s.io/cluster-api/api/v1beta1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func (r *reconciler) isCapiClusterReady(ctx context.Context, secret *corev1.Secret) bool {
	name := strings.ReplaceAll(secret.GetName(), "-kubeconfig", "")

	cl := meta.GetUnstructuredFromGVK(&schema.GroupVersionKind{Group: capiv1beta1.GroupVersion.Group, Version: capiv1beta1.GroupVersion.Version, Kind: reflect.TypeOf(capiv1beta1.Cluster{}).Name()})
	if err := r.Get(ctx, types.NamespacedName{Namespace: secret.GetNamespace(), Name: name}, cl); err != nil {
		r.l.Error(err, "cannot get cluster")
		return false
	}
	b, err := json.Marshal(cl)
	if err != nil {
		r.l.Error(err, "cannot marshal cluster")
		return false
	}
	cluster := &capiv1beta1.Cluster{}
	if err := json.Unmarshal(b, cluster); err != nil {
		r.l.Error(err, "cannot unmarshal cluster")
		return false
	}
	return isReady(cluster.GetConditions())
}

func getCapiClusterClient(secret *corev1.Secret) (applicator.APIPatchingApplicator, error) {
	//provide a restconfig from the secret value
	config, err := clientcmd.RESTConfigFromKubeConfig(secret.Data["value"])
	if err != nil {
		return applicator.APIPatchingApplicator{}, err
	}
	// build a cluster client from the kube rest config
	clClient, err := client.New(config, client.Options{})
	if err != nil {
		return applicator.APIPatchingApplicator{}, err
	}
	return applicator.NewAPIPatchingApplicator(clClient), nil
}

func isReady(cs capiv1beta1.Conditions) bool {
	for _, c := range cs {
		if c.Type == capiv1beta1.ReadyCondition {
			if c.Status == corev1.ConditionTrue {
				return true
			}
		}
	}
	return false
}

/*
func getReadyStatus(cs capiv1beta1.Conditions) capiv1beta1.Condition {
	for _, c := range cs {
		if c.Type == capiv1beta1.ReadyCondition {
			return c
		}
	}
	return capiv1beta1.Condition{}
}
*/
