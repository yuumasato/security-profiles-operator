/*
Copyright 2020 The Kubernetes Authors.

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

package bindata

import (
	appsv1 "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"sigs.k8s.io/security-profiles-operator/internal/pkg/config"
)

var (
	falsely                         = false
	truly                           = true
	userRoot                  int64 = 0
	userRootless                    = int64(config.UserRootless)
	hostCharDev                     = v1.HostPathCharDev
	hostPathDirectory               = v1.HostPathDirectory
	hostPathDirectoryOrCreate       = v1.HostPathDirectoryOrCreate
)

const (
	SelinuxDropDirectory = "/etc/selinux.d"
	SelinuxdPrivateDir   = "/var/run/selinuxd"
	SelinuxdSocketPath   = SelinuxdPrivateDir + "/selinuxd.sock"
	SelinuxdDBPath       = SelinuxdPrivateDir + "/selinuxd.db"
	kmsgPath             = "/dev/kmsg"
)

var Manifest = &appsv1.DaemonSet{
	ObjectMeta: metav1.ObjectMeta{
		Name:      config.OperatorName,
		Namespace: config.OperatorName,
	},
	Spec: appsv1.DaemonSetSpec{
		Selector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"app": "spod"},
		},
		Template: v1.PodTemplateSpec{
			ObjectMeta: metav1.ObjectMeta{
				Annotations: map[string]string{
					"openshift.io/scc":         "privileged",
					v1.SeccompPodAnnotationKey: v1.SeccompProfileRuntimeDefault,
					v1.SeccompContainerAnnotationKeyPrefix + config.OperatorName: "localhost/security-profiles-operator.json",
				},
				Labels: map[string]string{
					"app": "spod",
				},
			},
			Spec: v1.PodSpec{
				ServiceAccountName: config.OperatorName,
				InitContainers: []v1.Container{
					{
						Name:            "non-root-enabler",
						Args:            []string{"non-root-enabler"},
						ImagePullPolicy: v1.PullAlways,
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "host-varlib-volume",
								MountPath: "/var/lib",
							},
							{
								Name:      "operator-seccomp-profile-volume",
								MountPath: "/opt/seccomp-profiles",
								ReadOnly:  true,
							},
						},
						SecurityContext: &v1.SecurityContext{
							AllowPrivilegeEscalation: &falsely,
							ReadOnlyRootFilesystem:   &truly,
							Capabilities: &v1.Capabilities{
								Drop: []v1.Capability{"ALL"},
								Add:  []v1.Capability{"CHOWN", "FOWNER", "FSETID", "DAC_OVERRIDE"},
							},
							RunAsUser: &userRoot,
							SELinuxOptions: &v1.SELinuxOptions{
								// TODO(jaosorior): Use a more restricted selinux type
								Type: "spc_t",
							},
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("32Mi"),
								v1.ResourceCPU:              resource.MustParse("100m"),
								v1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
							},
							Limits: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("64Mi"),
								v1.ResourceCPU:              resource.MustParse("250m"),
								v1.ResourceEphemeralStorage: resource.MustParse("50Mi"),
							},
						},
					},
					{
						Name:  "selinux-shared-policies-copier",
						Image: "quay.io/jaosorior/selinuxd",
						// Primes the volume mount under /etc/selinux.d with the
						// shared policies shipped by selinuxd and makes sure the volume mount
						// is writable by 65535 in order for the controller to be able to
						// write the policy files. In the future, the policy files should
						// be shipped by selinuxd directly.
						//
						// The directory is writable by 65535 (the operator writes to this dir) and
						// readable by root (selinuxd reads the policies and runs as root).
						// Explicitly allowing root makes sure no dac_override audit messages
						// are logged even in absence of CAP_DAC_OVERRIDE.
						//
						// If, in the future we wanted to make the DS more compact, we could move
						// the chown+chmod to the previous init container and move the copying of
						// the files into a lifecycle handler of selinuxd.
						Command: []string{"bash", "-c"},
						Args: []string{`
						set -x

						chown 65535:0 /etc/selinux.d
						chmod 750 /etc/selinux.d
						cp /usr/share/udica/templates/* /etc/selinux.d
					`},
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "selinux-drop-dir",
								MountPath: SelinuxDropDirectory,
							},
						},
						SecurityContext: &v1.SecurityContext{
							AllowPrivilegeEscalation: &falsely,
							ReadOnlyRootFilesystem:   &truly,
							Capabilities: &v1.Capabilities{
								Drop: []v1.Capability{"ALL"},
								Add:  []v1.Capability{"CHOWN", "FOWNER", "FSETID", "DAC_OVERRIDE"},
							},
							RunAsUser: &userRoot,
							SELinuxOptions: &v1.SELinuxOptions{
								// TODO(jaosorior): Use a more restricted selinux type
								Type: "spc_t",
							},
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("32Mi"),
								v1.ResourceCPU:              resource.MustParse("100m"),
								v1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
							},
							Limits: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("64Mi"),
								v1.ResourceCPU:              resource.MustParse("250m"),
								v1.ResourceEphemeralStorage: resource.MustParse("50Mi"),
							},
						},
					},
				},
				Containers: []v1.Container{
					{
						Name:            config.OperatorName,
						Args:            []string{"daemon"},
						ImagePullPolicy: v1.PullAlways,
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "host-operator-volume",
								MountPath: config.ProfilesRootPath,
							},
							{
								Name:      "selinux-drop-dir",
								MountPath: SelinuxDropDirectory,
							},
							{
								Name:      "selinuxd-private-volume",
								MountPath: SelinuxdPrivateDir,
							},
							{
								Name:      "profile-recording-output-volume",
								MountPath: config.ProfileRecordingOutputPath,
							},
						},
						SecurityContext: &v1.SecurityContext{
							AllowPrivilegeEscalation: &falsely,
							ReadOnlyRootFilesystem:   &truly,
							Capabilities: &v1.Capabilities{
								Drop: []v1.Capability{"ALL"},
							},
							RunAsUser:  &userRootless,
							RunAsGroup: &userRootless,
							SELinuxOptions: &v1.SELinuxOptions{
								// TODO(jaosorior): Use a more restricted selinux type
								Type: "spc_t",
							},
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("64Mi"),
								v1.ResourceCPU:              resource.MustParse("100m"),
								v1.ResourceEphemeralStorage: resource.MustParse("50Mi"),
							},
							Limits: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("128Mi"),
								v1.ResourceCPU:              resource.MustParse("300m"),
								v1.ResourceEphemeralStorage: resource.MustParse("200Mi"),
							},
						},
						Env: []v1.EnvVar{
							{
								Name: config.NodeNameEnvKey,
								ValueFrom: &v1.EnvVarSource{
									FieldRef: &v1.ObjectFieldSelector{
										FieldPath: "spec.nodeName",
									},
								},
							},
						},
					},
					{
						Name:  "selinuxd",
						Image: "quay.io/jaosorior/selinuxd",
						Args: []string{
							"daemon",
							"--datastore-path", SelinuxdDbPath,
							"--socket-path", SelinuxdSocketPath,
							"--socket-uid", "0",
							"--socket-gid", "65535",
						},
						ImagePullPolicy: v1.PullAlways,
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "selinux-drop-dir",
								MountPath: SelinuxDropDirectory,
							},
							{
								Name:      "selinuxd-private-volume",
								MountPath: SelinuxdPrivateDir,
							},
							{
								Name:      "host-fsselinux-volume",
								MountPath: "/sys/fs/selinux",
							},
							{
								Name:      "host-etcselinux-volume",
								MountPath: "/etc/selinux",
							},
							{
								Name:      "host-varlibselinux-volume",
								MountPath: "/var/lib/selinux",
							},
						},
						SecurityContext: &v1.SecurityContext{
							ReadOnlyRootFilesystem:   &truly,
							RunAsUser:  &userRoot,
							RunAsGroup: &userRoot,
							SELinuxOptions: &v1.SELinuxOptions{
								// TODO(jaosorior): Use a more restricted selinux type
								Type: "spc_t",
							},
							/* TODO(jhrozek) is this really needed? */
							Privileged: &truly,
						},
						// selinuxd seems to be resource hungry, set later
						// Resources: v1.ResourceRequirements{
						//	Requests: v1.ResourceList{
						//		v1.ResourceMemory:           resource.MustParse("128Mi"),
						//		v1.ResourceCPU:              resource.MustParse("300m"),
						//		v1.ResourceEphemeralStorage: resource.MustParse("200Mi"),
						//	},
						//	Limits: v1.ResourceList{
						//		v1.ResourceMemory:           resource.MustParse("256Mi"),
						//		v1.ResourceCPU:              resource.MustParse("600m"),
						//		v1.ResourceEphemeralStorage: resource.MustParse("400Mi"),
						//	},
						// },
					},
					{
						Name:            "log-enricher",
						Args:            []string{"log-enricher"},
						ImagePullPolicy: v1.PullAlways,
						VolumeMounts: []v1.VolumeMount{
							{
								Name:      "host-devkmsg-volume",
								MountPath: kmsgPath,
								ReadOnly:  true,
							},
						},
						SecurityContext: &v1.SecurityContext{
							// /dev/kmsg is being used as kernel messages source in order to
							// support the widest range of distros. However, access to
							// devices requires the container to run as privileged.
							Privileged:             &truly,
							ReadOnlyRootFilesystem: &truly,
							SELinuxOptions: &v1.SELinuxOptions{
								// TODO(pjbgf): Use a more restricted selinux type
								Type: "spc_t",
							},
						},
						Resources: v1.ResourceRequirements{
							Requests: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("64Mi"),
								v1.ResourceCPU:              resource.MustParse("50m"),
								v1.ResourceEphemeralStorage: resource.MustParse("10Mi"),
							},
							Limits: v1.ResourceList{
								v1.ResourceMemory:           resource.MustParse("128Mi"),
								v1.ResourceCPU:              resource.MustParse("150m"),
								v1.ResourceEphemeralStorage: resource.MustParse("20Mi"),
							},
						},
						Env: []v1.EnvVar{
							{
								Name: "NODE_NAME",
								ValueFrom: &v1.EnvVarSource{
									FieldRef: &v1.ObjectFieldSelector{
										APIVersion: "v1",
										FieldPath:  "spec.nodeName",
									},
								},
							},
						},
					},
				},
				Volumes: []v1.Volume{
					// /var/lib is used as symlinks cannot be created across
					// different volumes
					{
						Name: "host-varlib-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: "/var/lib",
								Type: &hostPathDirectory,
							},
						},
					},
					{
						Name: "host-operator-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: "/var/lib/security-profiles-operator",
								Type: &hostPathDirectoryOrCreate,
							},
						},
					},
					{
						Name: "operator-seccomp-profile-volume",
						VolumeSource: v1.VolumeSource{
							ConfigMap: &v1.ConfigMapVolumeSource{
								LocalObjectReference: v1.LocalObjectReference{
									Name: "security-profiles-operator-profile",
								},
							},
						},
					},
					// Ephemeral emptyDirs for the selinuxd socket and the policies.
					// As the policies are loaded based on API objects and contents of
					// the init container and loaded on selinuxd start, we don't need
					// to persist them
					{
						Name: "selinux-drop-dir",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					},
					{
						Name: "selinuxd-private-volume",
						VolumeSource: v1.VolumeSource{
							EmptyDir: &v1.EmptyDirVolumeSource{},
						},
					},
					// The following host mounts only make sense on a SELinux enabled
					// system. But if SELinux is not configured, then they wouldn't be
					// used by any container, so it's OK to define them unconditionally
					{
						Name: "host-fsselinux-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: "/sys/fs/selinux",
								Type: &hostPathDirectory,
							},
						},
					},
					{
						Name: "host-etcselinux-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: "/etc/selinux",
								Type: &hostPathDirectory,
							},
						},
					},
					{
						Name: "host-varlibselinux-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: "/var/lib/selinux",
								Type: &hostPathDirectory,
							},
						},
					},
					{
						Name: "profile-recording-output-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: config.ProfileRecordingOutputPath,
								Type: &hostPathDirectoryOrCreate,
							},
						},
					},
					{
						Name: "host-devkmsg-volume",
						VolumeSource: v1.VolumeSource{
							HostPath: &v1.HostPathVolumeSource{
								Path: kmsgPath,
								Type: &hostCharDev,
							},
						},
					},
				},
				Tolerations: []v1.Toleration{
					{
						Effect: v1.TaintEffectNoSchedule,
						Key:    "node-role.kubernetes.io/master",
					},
					{
						Effect: v1.TaintEffectNoSchedule,
						Key:    "node-role.kubernetes.io/control-plane",
					},
					{
						Effect:   v1.TaintEffectNoExecute,
						Key:      "node.kubernetes.io/not-ready",
						Operator: v1.TolerationOpExists,
					},
				},
				NodeSelector: map[string]string{
					"kubernetes.io/os": "linux",
				},
			},
		},
	},
}
