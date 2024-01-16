/*
Copyright 2022.

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

package functional_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkv1 "github.com/k8snetworkplumbingwg/network-attachment-definition-client/pkg/apis/k8s.cni.cncf.io/v1"
	infranetworkv1 "github.com/openstack-k8s-operators/infra-operator/apis/network/v1beta1"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	"github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
	ovnv1 "github.com/openstack-k8s-operators/ovn-operator/api/v1beta1"
)

const (
	timeout  = time.Second * 10
	interval = timeout / 100
)

func GetDefaultOVNNorthdSpec() map[string]interface{} {
	return map[string]interface{}{
		"containerImage": "test-ovnnorthd-container-image",
	}
}

func CreateOVNNorthd(namespace string, OVNNorthdName string, spec map[string]interface{}) client.Object {

	raw := map[string]interface{}{
		"apiVersion": "ovn.openstack.org/v1beta1",
		"kind":       "OVNNorthd",
		"metadata": map[string]interface{}{
			"name":      OVNNorthdName,
			"namespace": namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func GetOVNNorthd(name types.NamespacedName) *ovnv1.OVNNorthd {
	instance := &ovnv1.OVNNorthd{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func OVNNorthdConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetOVNNorthd(name)
	return instance.Status.Conditions
}

func GetDefaultOVNDBClusterSpec() map[string]interface{} {
	return map[string]interface{}{
		"containerImage": "test-ovn-nb-container-image",
		"storageRequest": "1G",
		"storageClass":   "local-storage",
	}
}

func CreateOVNDBCluster(namespace string, OVNDBClusterName string, spec map[string]interface{}) client.Object {

	raw := map[string]interface{}{
		"apiVersion": "ovn.openstack.org/v1beta1",
		"kind":       "OVNDBCluster",
		"metadata": map[string]interface{}{
			"name":      OVNDBClusterName,
			"namespace": namespace,
		},
		"spec": spec,
	}
	return th.CreateUnstructured(raw)
}

func UpdateOVNDBCluster(cluster *ovnv1.OVNDBCluster) {
	k8sClient.Update(ctx, cluster)
}

func OVNDBClusterConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetOVNDBCluster(name)
	return instance.Status.Conditions
}

func ScaleDBCluster(name types.NamespacedName, replicas int32) {
	Eventually(func(g Gomega) {
		c := GetOVNDBCluster(name)
		*c.Spec.Replicas = replicas
		g.Expect(k8sClient.Update(ctx, c)).Should(Succeed())
	}).Should(Succeed())
}

// CreateOVNDBClusters Creates NB and SB OVNDBClusters
func CreateOVNDBClusters(namespace string, nad map[string][]string, replicas int32) []types.NamespacedName {
	dbs := []types.NamespacedName{}
	for _, db := range []string{v1beta1.NBDBType, v1beta1.SBDBType} {
		name := fmt.Sprintf("ovn-%s", uuid.New().String())
		spec := GetDefaultOVNDBClusterSpec()
		stringNad := ""
		// OVNDBCluster doesn't allow multiple NADs, hence map len
		// must be <= 1
		Expect(len(nad)).Should(BeNumerically("<=", 1))
		for k, _ := range nad {
			if strings.Contains(k, "/") {
				// k = namespace/nad_name, split[1] will return nad_name (e.g. internalapi)
				stringNad = strings.Split(k, "/")[1]
			}
		}
		if len(nad) != 0 {
			// nad format needs to be map[string][]string{namespace + "/" + nad_name: ...} or empty
			Expect(stringNad).ToNot(Equal(""))
		}
		spec["dbType"] = db
		spec["networkAttachment"] = stringNad
		spec["replicas"] = replicas

		instance := CreateOVNDBCluster(namespace, name, spec)

		instance_name := types.NamespacedName{Name: instance.GetName(), Namespace: instance.GetNamespace()}

		dbName := "nb"
		if db == v1beta1.SBDBType {
			dbName = "sb"
		}
		statefulSetName := types.NamespacedName{
			Namespace: instance.GetNamespace(),
			Name:      "ovsdbserver-" + dbName,
		}
		th.SimulateStatefulSetReplicaReadyWithPods(
			statefulSetName,
			nad,
		)
		// Ensure that PODs are ready and DBCluster have been reconciled
		// with all information (Status.DBAddress and internalDBAddress
		// are set at the end of the reconcileService)
		Eventually(func(g Gomega) {
			ovndbcluster := GetOVNDBCluster(instance_name)
			endpoint := ""
			// Check External endpoint when NAD is set
			if len(nad) == 0 {
				endpoint, _ = ovndbcluster.GetInternalEndpoint()
			} else {
				endpoint, _ = ovndbcluster.GetExternalEndpoint()
			}
			g.Expect(endpoint).ToNot(BeEmpty())
		}).Should(Succeed())

		dbs = append(dbs, instance_name)

	}

	logger.Info("OVNDBClusters created", "OVNDBCluster", dbs)
	return dbs
}

// DeleteOVNDBClusters Delete OVN DBClusters
func DeleteOVNDBClusters(names []types.NamespacedName) {
	for _, db := range names {
		th.DeleteInstance(GetOVNDBCluster(db))
	}
}

// GetOVNDBCluster Get OVNDBCluster
func GetOVNDBCluster(name types.NamespacedName) *ovnv1.OVNDBCluster {
	instance := &ovnv1.OVNDBCluster{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

// GetDaemonSet -
func GetDaemonSet(name types.NamespacedName) *appsv1.DaemonSet {
	ds := &appsv1.DaemonSet{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, ds)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return ds
}

// ListDaemonsets -
func ListDaemonsets(namespace string) *appsv1.DaemonSetList {
	dss := &appsv1.DaemonSetList{}
	Expect(k8sClient.List(ctx, dss, client.InNamespace(namespace))).Should(Succeed())
	return dss
}

// SimulateDaemonsetNumberReady -
func SimulateDaemonsetNumberReady(name types.NamespacedName) {
	Eventually(func(g Gomega) {
		ds := GetDaemonSet(name)
		ds.Status.NumberReady = 1
		ds.Status.DesiredNumberScheduled = 1
		g.Expect(k8sClient.Status().Update(ctx, ds)).To(Succeed())

	}, timeout, interval).Should(Succeed())
	logger.Info("Simulated daemonset success", "on", name)
}

func GetDefaultOVNControllerSpec() map[string]interface{} {
	return map[string]interface{}{}
}

func CreateOVNController(namespace string, OVNControllerName string, spec map[string]interface{}) client.Object {

	raw := map[string]interface{}{
		"apiVersion": "ovn.openstack.org/v1beta1",
		"kind":       "OVNController",
		"metadata": map[string]interface{}{
			"name":      OVNControllerName,
			"namespace": namespace,
		},
	}
	if len(spec) != 0 {
		raw["spec"] = spec
	}
	return th.CreateUnstructured(raw)
}

func GetOVNController(name types.NamespacedName) *ovnv1.OVNController {
	instance := &ovnv1.OVNController{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, instance)).Should(Succeed())
	}, timeout, interval).Should(Succeed())
	return instance
}

func OVNControllerConditionGetter(name types.NamespacedName) condition.Conditions {
	instance := GetOVNController(name)
	return instance.Status.Conditions
}

func SimulateDaemonsetNumberReadyWithPods(name types.NamespacedName, networkIPs map[string][]string) {
	ds := GetDaemonSet(name)

	for i := 0; i < int(1); i++ {
		pod := &corev1.Pod{
			ObjectMeta: ds.Spec.Template.ObjectMeta,
			Spec:       ds.Spec.Template.Spec,
		}
		pod.ObjectMeta.Namespace = name.Namespace
		pod.ObjectMeta.Name = name.Name
		pod.ObjectMeta.Labels = map[string]string{
			"service": "ovn-controller",
		}

		// NodeName required for getOVNControllerPods
		pod.Spec.NodeName = name.Name

		var netStatus []networkv1.NetworkStatus
		for network, IPs := range networkIPs {
			netStatus = append(
				netStatus,
				networkv1.NetworkStatus{
					Name: network,
					IPs:  IPs,
				},
			)
		}
		netStatusAnnotation, err := json.Marshal(netStatus)
		Expect(err).NotTo(HaveOccurred())
		pod.Annotations[networkv1.NetworkStatusAnnot] = string(netStatusAnnotation)
		Expect(k8sClient.Create(ctx, pod)).Should(Succeed())
	}

	Eventually(func(g Gomega) {
		ds := GetDaemonSet(name)
		ds.Status.NumberReady = 1
		ds.Status.DesiredNumberScheduled = 1
		g.Expect(k8sClient.Status().Update(ctx, ds)).To(Succeed())

	}, timeout, interval).Should(Succeed())

	logger.Info("Simulated daemonset success", "on", name)
}

func GetDNSData(name types.NamespacedName) *infranetworkv1.DNSData {
	dns := &infranetworkv1.DNSData{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, dns)).Should(Succeed())
	}).Should(Succeed())

	return dns
}

func GetDNSDataList(name types.NamespacedName, labelSelector string) *infranetworkv1.DNSDataList {
	dnsList := &infranetworkv1.DNSDataList{}
	dnsListOpts := client.ListOptions{
		Namespace: name.Namespace,
	}
	ml := client.MatchingLabels{
		"service": labelSelector,
	}
	ml.ApplyToList(&dnsListOpts)
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.List(ctx, dnsList, &dnsListOpts)).Should(Succeed())
	}).Should(Succeed())

	return dnsList
}

func GetDNSDataHostnameIP(dnsDataName string, namespace string, dnsHostname string) string {
	dnsEntry := GetDNSData(types.NamespacedName{Name: dnsDataName, Namespace: namespace})
	for _, host := range dnsEntry.Spec.Hosts {
		for i, hostname := range host.Hostnames {
			if hostname == dnsHostname {
				return dnsEntry.Spec.Hosts[i].IP
			}
		}
	}
	return ""
}

func GetPod(name types.NamespacedName) *corev1.Pod {
	pod := &corev1.Pod{}
	Eventually(func(g Gomega) {
		g.Expect(k8sClient.Get(ctx, name, pod)).Should(Succeed())
	}).Should(Succeed())

	return pod
}

func UpdatePod(pod *corev1.Pod) {
	k8sClient.Update(ctx, pod)
}
