/*
Copyright The Kubernetes Authors.

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

package e2enode

import (
	"context"
	"time"

	"github.com/onsi/ginkgo/v2"
	"github.com/onsi/gomega"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubernetes/test/e2e/framework"
	e2epod "k8s.io/kubernetes/test/e2e/framework/pod"
	admissionapi "k8s.io/pod-security-admission/api"
)

var _ = SIGDescribe("Shortened Grace Period", framework.WithNodeConformance(), func() {
	f := framework.NewDefaultFramework("shortened-grace-period")
	f.NamespacePodSecurityEnforceLevel = admissionapi.LevelBaseline

	ginkgo.Context("when a pod is deleted again with a shorter grace period", func() {
		var podClient *e2epod.PodClient
		ginkgo.BeforeEach(func() {
			podClient = e2epod.NewPodClient(f)
		})

		// The container ignores SIGTERM, so the first deletion keeps the kubelet
		// waiting for the whole grace period. A second deletion carrying a much
		// shorter grace period has to preempt that in-flight stop, otherwise the
		// pod would only disappear once the original grace period expired.
		ginkgo.It("should stop the pod before the original grace period elapses", func(ctx context.Context) {
			const (
				gracePeriod      = 100
				gracePeriodShort = 1
				firstStopTimeout = 30 * time.Second
				// shortenedDeleteTimeout is well below gracePeriod so the test fails if the first deletion is not preempted.
				shortenedDeleteTimeout = 30 * time.Second
			)
			podName := "shortened-grace-period-test"
			podClient.CreateSync(ctx, getGracePeriodTestPod(podName, gracePeriod))

			ginkgo.By("deleting the pod with the original grace period")
			err := podClient.Delete(ctx, podName, *metav1.NewDeleteOptions(gracePeriod))
			framework.ExpectNoError(err, "failed to delete pod with the original grace period")

			// Waiting for the first SIGTERM makes the test exercise cancellation of
			// an in-flight runtime stop instead of racing both deletes ahead of it.
			ginkgo.By("waiting for the first container stop to be in flight")
			gomega.Eventually(ctx, func(ctx context.Context) (string, error) {
				logs, err := podClient.GetLogs(podName, &v1.PodLogOptions{Container: podName}).DoRaw(ctx)
				return string(logs), err
			}, firstStopTimeout, time.Second).Should(gomega.ContainSubstring("ignoring SIGTERM"))

			ginkgo.By("deleting the pod again with a shorter grace period")
			start := time.Now()
			podClient.DeleteSync(ctx, podName, *metav1.NewDeleteOptions(gracePeriodShort), shortenedDeleteTimeout)
			framework.Logf("pod disappeared %v after the shortened delete", time.Since(start))
		})
	})
})

// getGracePeriodTestPod returns a pod whose container traps and ignores
// SIGTERM, so that it is only removed once its grace period has expired and
// the container runtime sends SIGKILL.
func getGracePeriodTestPod(name string, gracePeriod int64) *v1.Pod {
	pod := &v1.Pod{
		TypeMeta: metav1.TypeMeta{
			Kind:       "Pod",
			APIVersion: "v1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: v1.PodSpec{
			RestartPolicy: v1.RestartPolicyNever,
			Containers: []v1.Container{
				{
					Name:    name,
					Image:   busyboxImage,
					Command: []string{"sh", "-c"},
					Args: []string{`
trap 'echo ignoring SIGTERM' TERM
touch /tmp/sigterm-handler-ready
while true; do sleep 1; done
`},
					ReadinessProbe: &v1.Probe{
						PeriodSeconds: 1,
						ProbeHandler: v1.ProbeHandler{
							Exec: &v1.ExecAction{Command: []string{"sh", "-c", "test -f /tmp/sigterm-handler-ready"}},
						},
					},
				},
			},
			TerminationGracePeriodSeconds: &gracePeriod,
		},
	}
	return pod
}
