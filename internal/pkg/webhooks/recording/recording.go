/*
Copyright 2021 The Kubernetes Authors.

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

package recording

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/go-logr/logr"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	profilerecordingv1alpha1 "sigs.k8s.io/security-profiles-operator/api/profilerecording/v1alpha1"
	"sigs.k8s.io/security-profiles-operator/internal/pkg/config"
	"sigs.k8s.io/security-profiles-operator/internal/pkg/util"
	"sigs.k8s.io/security-profiles-operator/internal/pkg/webhooks/utils"
)

const finalizer = "active-seccomp-profile-recording-lock"

type podSeccompRecorder struct {
	impl
	log      logr.Logger
	replicas sync.Map
}

func RegisterWebhook(server *webhook.Server, c client.Client) {
	server.Register(
		"/mutate-v1-pod-recording",
		&webhook.Admission{
			Handler: &podSeccompRecorder{
				impl: &defaultImpl{client: c},
				log:  logf.Log.WithName("recording"),
			},
		},
	)
}

//nolint:lll // required for kubebuilder
// Security Profiles Operator Webhook RBAC permissions
// +kubebuilder:rbac:groups=security-profiles-operator.x-k8s.io,resources=profilerecordings,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=security-profiles-operator.x-k8s.io,resources=profilerecordings/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=security-profiles-operator.x-k8s.io,resources=profilerecordings/finalizers,verbs=delete;get;update;patch
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch

//nolint:gocritic
func (p *podSeccompRecorder) Handle(
	ctx context.Context,
	req admission.Request,
) admission.Response {
	profileRecordings, err := p.impl.ListProfileRecordings(
		ctx, client.InNamespace(req.Namespace),
	)
	if err != nil {
		p.log.Error(err, "Could not list profile recordings")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	podChanged := false
	pod := &corev1.Pod{}
	if req.Operation != admissionv1.Delete {
		pod, err = p.impl.DecodePod(req)
		if err != nil {
			p.log.Error(err, "Failed to decode pod")
			return admission.Errored(http.StatusBadRequest, err)
		}
	}

	podName := req.Name
	if podName == "" {
		podName = pod.GenerateName
	}

	podLabels := labels.Set(pod.GetLabels())
	items := profileRecordings.Items

	for i := range items {
		item := items[i]
		if !item.IsKindSupported() {
			p.log.Info(fmt.Sprintf(
				"recording kind %s not supported", item.Spec.Kind,
			))
			continue
		}

		selector, err := p.impl.LabelSelectorAsSelector(
			&item.Spec.PodSelector,
		)
		if err != nil {
			p.log.Error(
				err, "Could not get label selector from profile recording",
			)
			return admission.Errored(http.StatusInternalServerError, err)
		}

		recordedPods, err := p.impl.ListRecordedPods(ctx, req.Namespace, &item.Spec.PodSelector)
		if err != nil {
			p.log.Error(
				err, "Could not list pods for this recording",
			)
			return admission.Errored(http.StatusInternalServerError, err)
		}

		if err := util.Retry(func() error {
			if err := p.setRecordingReferences(ctx, req.Operation,
				&item, selector,
				podName, podLabels,
				recordedPods); err != nil {
				return fmt.Errorf("adding pod tracking: %w", err)
			}

			return nil
		}, kerrors.IsConflict); err != nil {
			return admission.Errored(http.StatusInternalServerError, err)
		}

		if selector.Matches(podLabels) {
			podChanged, err = p.updatePod(pod, podName, &item)
			if err != nil {
				return admission.Errored(http.StatusInternalServerError, err)
			}
		}
	}

	if !podChanged {
		return admission.Allowed("pod unchanged")
	}

	marshaledPod, err := json.Marshal(pod)
	if err != nil {
		p.log.Error(err, "Failed to encode pod")
		return admission.Errored(http.StatusInternalServerError, err)
	}

	return admission.PatchResponseFromRaw(req.Object.Raw, marshaledPod)
}

func (p *podSeccompRecorder) shouldRecordContainer(containerName string,
	profileRecording *profilerecordingv1alpha1.ProfileRecording,
) bool {
	// Allow all containers when no containers are explicitly listed
	if profileRecording.Spec.Containers == nil {
		return true
	}
	return util.Contains(profileRecording.Spec.Containers, containerName)
}

func (p *podSeccompRecorder) updatePod(
	pod *corev1.Pod,
	podName string,
	profileRecording *profilerecordingv1alpha1.ProfileRecording,
) (podChanged bool, err error) {
	// Collect containers as references to not copy them during modification
	ctrs := []*corev1.Container{}
	for i := range pod.Spec.InitContainers {
		if p.shouldRecordContainer(pod.Spec.InitContainers[i].Name, profileRecording) {
			ctrs = append(ctrs, &pod.Spec.InitContainers[i])
		}
	}
	for i := range pod.Spec.Containers {
		if p.shouldRecordContainer(pod.Spec.Containers[i].Name, profileRecording) {
			ctrs = append(ctrs, &pod.Spec.Containers[i])
		}
	}

	// Handle replicas by tracking them
	replica, err := p.getReplica(pod, profileRecording)
	if err != nil {
		return false, err
	}

	for i := range ctrs {
		ctr := ctrs[i]

		key, value, err := profileRecording.CtrAnnotation(replica, ctr.Name)
		if err != nil {
			return false, err
		}

		p.updateSecurityContext(ctr, profileRecording)
		existingValue, ok := pod.GetAnnotations()[key]
		if !ok {
			if pod.Annotations == nil {
				pod.Annotations = make(map[string]string)
			}
			pod.Annotations[key] = value
			p.log.Info(fmt.Sprintf(
				"adding recording annotation %s=%s to pod %s",
				key, value, pod.Name,
			))
			podChanged = true
			continue
		}

		if existingValue != value {
			p.log.Error(
				errors.New("existing annotation"),
				fmt.Sprintf(
					"workload %s already has annotation %s (not mutating to %s).",
					podName,
					existingValue,
					value,
				),
			)
		}
	}

	return podChanged, nil
}

func (p *podSeccompRecorder) updateSecurityContext(
	ctr *corev1.Container, pr *profilerecordingv1alpha1.ProfileRecording,
) {
	if pr.Spec.Recorder != profilerecordingv1alpha1.ProfileRecorderLogs {
		// we only need to ensure the special security context if we're tailing
		// the logs
		return
	}

	switch pr.Spec.Kind {
	case profilerecordingv1alpha1.ProfileRecordingKindSeccompProfile:
		p.updateSeccompSecurityContext(ctr)
	case profilerecordingv1alpha1.ProfileRecordingKindSelinuxProfile:
		p.updateSelinuxSecurityContext(ctr)
	}

	p.log.Info(fmt.Sprintf(
		"set SecurityContext for container %s: %+v",
		ctr.Name, ctr.SecurityContext,
	))
}

func (p *podSeccompRecorder) updateSeccompSecurityContext(
	ctr *corev1.Container,
) {
	if ctr.SecurityContext == nil {
		ctr.SecurityContext = &corev1.SecurityContext{}
	}

	if ctr.SecurityContext.SeccompProfile == nil {
		ctr.SecurityContext.SeccompProfile = &corev1.SeccompProfile{}
	}

	ctr.SecurityContext.SeccompProfile.Type = corev1.SeccompProfileTypeLocalhost
	profile := fmt.Sprintf(
		"operator/%s/%s.json",
		p.impl.GetOperatorNamespace(),
		config.LogEnricherProfile,
	)
	ctr.SecurityContext.SeccompProfile.LocalhostProfile = &profile
}

func (p *podSeccompRecorder) updateSelinuxSecurityContext(
	ctr *corev1.Container,
) {
	if ctr.SecurityContext == nil {
		ctr.SecurityContext = &corev1.SecurityContext{}
	}

	if ctr.SecurityContext.SELinuxOptions == nil {
		ctr.SecurityContext.SELinuxOptions = &corev1.SELinuxOptions{}
	}

	ctr.SecurityContext.SELinuxOptions.Type = config.SelinuxPermissiveProfile
}

func replicaKey(recordingName, podName string) string {
	return fmt.Sprintf("%s/%s", recordingName, podName)
}

func (p *podSeccompRecorder) cleanupReplicas(recordingName, podName string) {
	rKey := replicaKey(recordingName, podName)

	p.replicas.Range(func(key, _ interface{}) bool {
		keyString, ok := key.(string)
		if !ok {
			return false
		}
		if strings.HasPrefix(rKey, keyString) {
			p.replicas.Delete(key)
			return false
		}
		return true
	})
}

func (p *podSeccompRecorder) getReplica(
	pod *corev1.Pod,
	profileRecording *profilerecordingv1alpha1.ProfileRecording,
) (string, error) {
	replica := ""
	if pod.Name == "" && pod.GenerateName != "" {
		rKey := replicaKey(profileRecording.Name, pod.GenerateName)

		v, _ := p.replicas.LoadOrStore(rKey, uint(0))
		replica = fmt.Sprintf("-%d", v)
		vUint, ok := v.(uint)
		if !ok {
			return "", errors.New("replicas value is not an uint")
		}
		p.replicas.Store(rKey, vUint+1)
	}

	return replica, nil
}

func (p *podSeccompRecorder) setRecordingReferences(
	ctx context.Context,
	op admissionv1.Operation,
	profileRecording *profilerecordingv1alpha1.ProfileRecording,
	selector labels.Selector,
	podName string,
	podLabels labels.Set,
	recordedPods *corev1.PodList,
) error {
	// we Get the recording again because remove is used in a retry loop
	// to handle conflicts, we want to get the most recent one
	profileRecording, err := p.impl.GetProfileRecording(ctx, profileRecording.Name, profileRecording.Namespace)
	if kerrors.IsNotFound(err) {
		// this can happen if the profile recording is deleted while we're reconciling
		// just return without doing anything
		return nil
	} else if err != nil {
		return fmt.Errorf("cannot retrieve profilerecording: %w", err)
	}

	if err := p.setActiveWorkloads(ctx, op, profileRecording, selector, podName, podLabels, recordedPods); err != nil {
		return fmt.Errorf("cannot set active workloads: %w", err)
	}

	return p.setFinalizers(ctx, op, profileRecording, selector, podName, podLabels, recordedPods)
}

func (p *podSeccompRecorder) setActiveWorkloads(
	ctx context.Context,
	op admissionv1.Operation,
	profileRecording *profilerecordingv1alpha1.ProfileRecording,
	selector labels.Selector,
	podName string,
	podLabels labels.Set,
	recordedPods *corev1.PodList,
) error {
	newActiveWorkloads := []string{}

	for _, pod := range recordedPods.Items {
		newActiveWorkloads = append(newActiveWorkloads, pod.Name)
	}

	if op == admissionv1.Delete {
		newActiveWorkloads = utils.RemoveIfExists(newActiveWorkloads, podName)
	} else {
		if selector.Matches(podLabels) {
			newActiveWorkloads = utils.AppendIfNotExists(newActiveWorkloads, podName)
		}
	}

	profileRecording.Status.ActiveWorkloads = newActiveWorkloads

	return p.impl.UpdateResourceStatus(ctx, p.log, profileRecording, "profilerecording status")
}

func (p *podSeccompRecorder) setFinalizers(
	ctx context.Context,
	op admissionv1.Operation,
	profileRecording *profilerecordingv1alpha1.ProfileRecording,
	selector labels.Selector,
	podName string,
	podLabels labels.Set,
	recordedPods *corev1.PodList,
) error {
	if anyPodMatch(op, selector, podLabels, podName, recordedPods) {
		if !controllerutil.ContainsFinalizer(profileRecording, finalizer) {
			controllerutil.AddFinalizer(profileRecording, finalizer)
		}
	} else {
		if controllerutil.ContainsFinalizer(profileRecording, finalizer) {
			controllerutil.RemoveFinalizer(profileRecording, finalizer)
			p.cleanupReplicas(profileRecording.Name, podName)
		}
	}

	return p.impl.UpdateResource(ctx, p.log, profileRecording, "profilerecording")
}

func anyPodMatch(
	op admissionv1.Operation,
	selector labels.Selector,
	podLabels labels.Set,
	podName string,
	foundPods *corev1.PodList,
) bool {
	if len(foundPods.Items) > 0 {
		if op != admissionv1.Delete {
			return true
		}

		for _, pod := range foundPods.Items {
			if podName == pod.Name {
				continue
			}
			// if we ever get here, then we are deleting but we encountered
			// a pod different than the one we're deleting, so there are still pods
			return true
		}
	}

	if op != admissionv1.Delete && selector.Matches(podLabels) {
		return true
	}

	return false
}

func (p *podSeccompRecorder) InjectDecoder(decoder *admission.Decoder) error {
	p.impl.SetDecoder(decoder)
	return nil
}
