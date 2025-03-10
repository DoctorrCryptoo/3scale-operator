package reconcilers

import (
	"fmt"
	corev1 "k8s.io/api/core/v1"
	"reflect"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	k8sappsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/3scale/3scale-operator/pkg/common"
	"github.com/3scale/3scale-operator/pkg/helper"
)

const (
	DeploymentKind          = "Deployment"
	DeploymentAPIVersion    = "apps/v1"
	DeploymentLabelSelector = "deployment"
)

type ContainerImage struct {
	Name string
	Tag  string
}

type ImageTriggerFrom struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	// +optional
	Namespace *string `json:"namespace,omitempty"`
}

type ImageTrigger struct {
	From      ImageTriggerFrom `json:"from"`
	FieldPath string           `json:"fieldPath"`
	// +optional
	Paused bool `json:"paused,omitempty"`
}

// DMutateFn is a function which mutates the existing Deployment into it's desired state.
type DMutateFn func(desired, existing *k8sappsv1.Deployment) (bool, error)

func DeploymentMutator(opts ...DMutateFn) MutateFn {
	return func(existingObj, desiredObj common.KubernetesObject) (bool, error) {
		existing, ok := existingObj.(*k8sappsv1.Deployment)
		if !ok {
			return false, fmt.Errorf("%T is not a *k8sappsv1.Deployment", existingObj)
		}
		desired, ok := desiredObj.(*k8sappsv1.Deployment)
		if !ok {
			return false, fmt.Errorf("%T is not a *k8sappsv1.Deployment", desiredObj)
		}

		update := false

		// Loop through each option
		for _, opt := range opts {
			tmpUpdate, err := opt(desired, existing)
			if err != nil {
				return false, err
			}
			update = update || tmpUpdate
		}

		return update, nil
	}
}

// GenericBackendDeploymentMutators returns the generic mutators for backend
func GenericBackendDeploymentMutators() []DMutateFn {
	return []DMutateFn{
		DeploymentAnnotationsMutator,
		DeploymentContainerResourcesMutator,
		DeploymentAffinityMutator,
		DeploymentTolerationsMutator,
		DeploymentPodTemplateLabelsMutator,
		DeploymentPriorityClassMutator,
		DeploymentTopologySpreadConstraintsMutator,
		DeploymentPodTemplateAnnotationsMutator,
		DeploymentArgsMutator,
		DeploymentProbesMutator,
		DeploymentPodContainerImageMutator,
		DeploymentPodInitContainerImageMutator,
	}
}

// DeploymentAnnotationsMutator ensures Deployment Annotations are reconciled
func DeploymentAnnotationsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	helper.MergeMapStringString(&updated, &existing.ObjectMeta.Annotations, desired.ObjectMeta.Annotations)

	return updated, nil
}

func DeploymentReplicasMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	update := false

	if desired.Spec.Replicas != existing.Spec.Replicas {
		existing.Spec.Replicas = desired.Spec.Replicas
		update = true
	}

	return update, nil
}

func DeploymentAffinityMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	if !reflect.DeepEqual(existing.Spec.Template.Spec.Affinity, desired.Spec.Template.Spec.Affinity) {
		diff := cmp.Diff(existing.Spec.Template.Spec.Affinity, desired.Spec.Template.Spec.Affinity)
		log.Info(fmt.Sprintf("%s spec.template.spec.Affinity has changed: %s", common.ObjectInfo(desired), diff))
		existing.Spec.Template.Spec.Affinity = desired.Spec.Template.Spec.Affinity
		updated = true
	}

	return updated, nil
}

func DeploymentTolerationsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	if !reflect.DeepEqual(existing.Spec.Template.Spec.Tolerations, desired.Spec.Template.Spec.Tolerations) {
		diff := cmp.Diff(existing.Spec.Template.Spec.Tolerations, desired.Spec.Template.Spec.Tolerations)
		log.Info(fmt.Sprintf("%s spec.template.spec.Tolerations has changed: %s", common.ObjectInfo(desired), diff))
		existing.Spec.Template.Spec.Tolerations = desired.Spec.Template.Spec.Tolerations
		updated = true
	}

	return updated, nil
}

func DeploymentContainerResourcesMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	desiredName := common.ObjectInfo(desired)
	update := false

	if len(desired.Spec.Template.Spec.Containers) != 1 {
		return false, fmt.Errorf("%s desired spec.template.spec.containers length changed to '%d', should be 1", desiredName, len(desired.Spec.Template.Spec.Containers))
	}

	if len(existing.Spec.Template.Spec.Containers) != 1 {
		log.Info(fmt.Sprintf("%s spec.template.spec.containers length changed to '%d', recreating dc", desiredName, len(existing.Spec.Template.Spec.Containers)))
		existing.Spec.Template.Spec.Containers = desired.Spec.Template.Spec.Containers
		update = true
	}

	if !helper.CmpResources(&existing.Spec.Template.Spec.Containers[0].Resources, &desired.Spec.Template.Spec.Containers[0].Resources) {
		diff := cmp.Diff(existing.Spec.Template.Spec.Containers[0].Resources, desired.Spec.Template.Spec.Containers[0].Resources, cmpopts.IgnoreUnexported(resource.Quantity{}))
		log.Info(fmt.Sprintf("%s spec.template.spec.containers[0].resources have changed: %s", desiredName, diff))
		existing.Spec.Template.Spec.Containers[0].Resources = desired.Spec.Template.Spec.Containers[0].Resources
		update = true
	}

	return update, nil
}

// DeploymentEnvVarReconciler implements basic env var reconciliation deployments.
// Existing and desired Deployment must have same number of containers
// Added when in desired and not in existing
// Updated when in desired and in existing but not equal
// Removed when not in desired and exists in existing Deployment
func DeploymentEnvVarReconciler(desired, existing *k8sappsv1.Deployment, envVar string) bool {
	updated := false

	if len(desired.Spec.Template.Spec.Containers) != len(existing.Spec.Template.Spec.Containers) {
		log.Info("[WARNING] not reconciling deployment",
			"name", client.ObjectKeyFromObject(desired),
			"reason", "existing and desired do not have same number of containers")
		return false
	}

	if len(desired.Spec.Template.Spec.InitContainers) != len(existing.Spec.Template.Spec.InitContainers) {
		log.Info("[WARNING] not reconciling deployment",
			"name", client.ObjectKeyFromObject(desired),
			"reason", "existing and desired do not have same number of init containers")
		return false
	}

	// Init Containers
	for idx := range existing.Spec.Template.Spec.InitContainers {
		tmpChanged := helper.EnvVarReconciler(
			desired.Spec.Template.Spec.InitContainers[idx].Env,
			&existing.Spec.Template.Spec.InitContainers[idx].Env,
			envVar)
		updated = updated || tmpChanged
	}

	// Containers
	for idx := range existing.Spec.Template.Spec.Containers {
		tmpChanged := helper.EnvVarReconciler(
			desired.Spec.Template.Spec.Containers[idx].Env,
			&existing.Spec.Template.Spec.Containers[idx].Env,
			envVar)
		updated = updated || tmpChanged
	}

	return updated
}

// DeploymentPodTemplateLabelsMutator ensures pod template labels are reconciled
func DeploymentPodTemplateLabelsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	helper.MergeMapStringString(&updated, &existing.Spec.Template.Labels, desired.Spec.Template.Labels)

	return updated, nil
}

// DeploymentRemoveDuplicateEnvVarMutator ensures pod env vars are not duplicated
func DeploymentRemoveDuplicateEnvVarMutator(_, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false
	for idx := range existing.Spec.Template.Spec.Containers {
		prunedEnvs := helper.RemoveDuplicateEnvVars(existing.Spec.Template.Spec.Containers[idx].Env)
		if !reflect.DeepEqual(existing.Spec.Template.Spec.Containers[idx].Env, prunedEnvs) {
			existing.Spec.Template.Spec.Containers[idx].Env = prunedEnvs
			updated = true
		}
	}

	return updated, nil
}

// DeploymentPriorityClassMutator ensures priorityclass is reconciled
func DeploymentPriorityClassMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	if existing.Spec.Template.Spec.PriorityClassName != desired.Spec.Template.Spec.PriorityClassName {
		existing.Spec.Template.Spec.PriorityClassName = desired.Spec.Template.Spec.PriorityClassName
		updated = true
	}

	return updated, nil
}

// DeploymentStrategyMutator ensures desired strategy
func DeploymentStrategyMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	if !reflect.DeepEqual(existing.Spec.Strategy, desired.Spec.Strategy) {
		existing.Spec.Strategy = desired.Spec.Strategy
		updated = true
	}

	return updated, nil
}

// DeploymentTopologySpreadConstraintsMutator ensures TopologySpreadConstraints is reconciled
func DeploymentTopologySpreadConstraintsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	if !reflect.DeepEqual(existing.Spec.Template.Spec.TopologySpreadConstraints, desired.Spec.Template.Spec.TopologySpreadConstraints) {
		diff := cmp.Diff(existing.Spec.Template.Spec.TopologySpreadConstraints, desired.Spec.Template.Spec.TopologySpreadConstraints)
		log.Info(fmt.Sprintf("%s spec.template.spec.TopologySpreadConstraints has changed: %s", common.ObjectInfo(desired), diff))
		existing.Spec.Template.Spec.TopologySpreadConstraints = desired.Spec.Template.Spec.TopologySpreadConstraints
		updated = true
	}

	return updated, nil
}

// DeploymentPodTemplateAnnotationsMutator ensures Pod Template Annotations is reconciled
func DeploymentPodTemplateAnnotationsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	helper.MergeMapStringString(&updated, &existing.Spec.Template.Annotations, desired.Spec.Template.Annotations)

	return updated, nil
}

// DeploymentArgsMutator ensures deployment's containers' args are reconciled
func DeploymentArgsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	for i, desiredContainer := range desired.Spec.Template.Spec.Containers {
		existingContainer := &existing.Spec.Template.Spec.Containers[i]

		if !reflect.DeepEqual(existingContainer.Args, desiredContainer.Args) {
			existingContainer.Args = desiredContainer.Args
			updated = true
		}
	}

	return updated, nil
}

// DeploymentProbesMutator ensures probes are reconciled
func DeploymentProbesMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	for i, desiredContainer := range desired.Spec.Template.Spec.Containers {
		existingContainer := &existing.Spec.Template.Spec.Containers[i]

		if !reflect.DeepEqual(existingContainer.LivenessProbe, desiredContainer.LivenessProbe) {
			existingContainer.LivenessProbe = desiredContainer.LivenessProbe
			updated = true
		}

		if !reflect.DeepEqual(existingContainer.ReadinessProbe, desiredContainer.ReadinessProbe) {
			existingContainer.ReadinessProbe = desiredContainer.ReadinessProbe
			updated = true
		}
	}

	return updated, nil
}

// DeploymentPodContainerImageMutator ensures that the deployment's pod's containers are reconciled
func DeploymentPodContainerImageMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	for i, desiredContainer := range desired.Spec.Template.Spec.Containers {
		existingContainer := &existing.Spec.Template.Spec.Containers[i]

		if !reflect.DeepEqual(existingContainer.Image, desiredContainer.Image) {
			existingContainer.Image = desiredContainer.Image
			updated = true
		}
	}
	return updated, nil
}

// DeploymentPodInitContainerImageMutator ensures that the deployment's pod's containers are reconciled
func DeploymentPodInitContainerImageMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	for i, desiredContainer := range desired.Spec.Template.Spec.InitContainers {
		if i >= len(existing.Spec.Template.Spec.InitContainers) {
			// Add missing containers from desired to existing
			existing.Spec.Template.Spec.InitContainers = append(existing.Spec.Template.Spec.InitContainers, desiredContainer)
			fmt.Printf("Added missing container: %s\n", desiredContainer.Name)
			updated = true
			continue
		}
		existingContainer := &existing.Spec.Template.Spec.InitContainers[i]

		if !reflect.DeepEqual(existingContainer.Image, desiredContainer.Image) {
			existingContainer.Image = desiredContainer.Image
			updated = true
		}
	}
	return updated, nil
}

func DeploymentListenerArgsMutator(_, existing *k8sappsv1.Deployment) (bool, error) {
	update := true
	falconArgs := []string{"bin/3scale_backend", "-s", "falcon", "start", "-e", "production", "-p", "3000", "-x", "/dev/stdout"}
	if !reflect.DeepEqual(existing.Spec.Template.Spec.Containers[0].Args, falconArgs) {
		existing.Spec.Template.Spec.Containers[0].Args = falconArgs
		return update, nil
	}
	update = false
	return update, nil
}
func DeploymentListenerAsyncDisableArgsMutator(_, existing *k8sappsv1.Deployment) (bool, error) {
	update := true
	falconArgs := []string{"bin/3scale_backend", "start", "-e", "production", "-p", "3000", "-x", "/dev/stdout"}
	if !reflect.DeepEqual(existing.Spec.Template.Spec.Containers[0].Args, falconArgs) {
		existing.Spec.Template.Spec.Containers[0].Args = falconArgs
		return update, nil
	}
	update = false
	return update, nil
}

func DeploymentListenerAsyncDisableEnvMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	update := false
	updateListenerWorkers := true
	updateConfigRedisAsync := true
	// This may be redundant as operator crashes if LISTENER_WORKERS=0
	// Update LISTENER_WORKERS and CONFIG_REDIS_ASYNC to 1 if found
	for envId, envVar := range existing.Spec.Template.Spec.Containers[0].Env {
		if envVar.Name == "LISTENER_WORKERS" {
			updateListenerWorkers = false
			if envVar.Value == "1" {
				existing.Spec.Template.Spec.Containers[0].Env = removeEnvVar(existing.Spec.Template.Spec.Containers[0].Env, "LISTENER_WORKERS")
				update = true
			}
		}
		if envVar.Name == "CONFIG_REDIS_ASYNC" {
			updateConfigRedisAsync = false
			if envVar.Value == "1" {
				existing.Spec.Template.Spec.Containers[0].Env[envId].Value = "0"
				update = true
			}
		}
		if update {
			return update, nil
		}
	}
	// if either updateListenerWorkers or updateConfigRedisAsync is true then proceed to the append logic
	// to add the env var LISTENER_WORKERS and CONFIG_REDIS_ASYNC
	if updateListenerWorkers || updateConfigRedisAsync {
		update = true
	} else {
		update = false
	}
	if updateConfigRedisAsync {
		existing.Spec.Template.Spec.Containers[0].Env = append(existing.Spec.Template.Spec.Containers[0].Env,
			helper.EnvVarFromValue("CONFIG_REDIS_ASYNC", "0"))
	}
	if updateListenerWorkers {
		existing.Spec.Template.Spec.Containers[0].Env = removeEnvVar(existing.Spec.Template.Spec.Containers[0].Env, "LISTENER_WORKERS")
	}

	return update, nil
}

func DeploymentListenerEnvMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	update := false
	updateListenerWorkers := true
	updateConfigRedisAsync := true
	// This may be redundant as operator crashes if LISTENER_WORKERS=0
	// Update LISTENER_WORKERS and CONFIG_REDIS_ASYNC to 1 if found
	for envId, envVar := range existing.Spec.Template.Spec.Containers[0].Env {
		if envVar.Name == "LISTENER_WORKERS" {
			updateListenerWorkers = false
			if envVar.Value == "0" {
				existing.Spec.Template.Spec.Containers[0].Env[envId].Value = "1"
				update = true
			}
		}
		if envVar.Name == "CONFIG_REDIS_ASYNC" {
			updateConfigRedisAsync = false
			if envVar.Value == "0" {
				existing.Spec.Template.Spec.Containers[0].Env[envId].Value = "1"
				update = true

			}
		}
		if update {
			return update, nil
		}
	}
	// if either updateListenerWorkers or updateConfigRedisAsync is true then proceed to the append logic
	// to add the env var LISTENER_WORKERS and CONFIG_REDIS_ASYNC
	if updateListenerWorkers || updateConfigRedisAsync {
		update = true
	} else {
		update = false
	}
	if updateConfigRedisAsync {
		existing.Spec.Template.Spec.Containers[0].Env = append(existing.Spec.Template.Spec.Containers[0].Env,
			helper.EnvVarFromValue("CONFIG_REDIS_ASYNC", "1"))
	}
	if updateListenerWorkers {
		existing.Spec.Template.Spec.Containers[0].Env = append(existing.Spec.Template.Spec.Containers[0].Env,
			helper.EnvVarFromValue("LISTENER_WORKERS", "1"))
	}

	return update, nil
}

func DeploymentWorkerEnvMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	update := true
	// Always set env var CONFIG_REDIS_ASYNC to 1 this logic is only hit when you don't have logical redis db
	for envId, envVar := range existing.Spec.Template.Spec.Containers[0].Env {
		if envVar.Name == "CONFIG_REDIS_ASYNC" {
			if envVar.Value == "0" {
				existing.Spec.Template.Spec.Containers[0].Env[envId].Value = "1"
				update = true
				return update, nil
			}
			update = false

		}
	}
	// Adds the env CONFIG_REDIS_ASYNC if not present
	if update {
		existing.Spec.Template.Spec.Containers[0].Env = append(existing.Spec.Template.Spec.Containers[0].Env,
			helper.EnvVarFromValue("CONFIG_REDIS_ASYNC", "1"))
	}
	return update, nil
}

func DeploymentWorkerDisableAsyncEnvMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	update := true
	// Always set env var CONFIG_REDIS_ASYNC to 1 this logic is only hit when you don't have logical redis db
	for envId, envVar := range existing.Spec.Template.Spec.Containers[0].Env {
		if envVar.Name == "CONFIG_REDIS_ASYNC" {
			if envVar.Value == "1" {
				existing.Spec.Template.Spec.Containers[0].Env[envId].Value = "0"
				update = true
				return update, nil
			}
			update = false

		}
	}
	// Adds the env CONFIG_REDIS_ASYNC if not present
	if update {
		existing.Spec.Template.Spec.Containers[0].Env = append(existing.Spec.Template.Spec.Containers[0].Env,
			helper.EnvVarFromValue("CONFIG_REDIS_ASYNC", "0"))
	}
	return update, nil
}

func removeEnvVar(envVars []corev1.EnvVar, name string) []corev1.EnvVar {
	var newEnvVars []corev1.EnvVar
	for _, envVar := range envVars {
		if envVar.Name != name {
			newEnvVars = append(newEnvVars, envVar)
		}
	}
	return newEnvVars
}

// DeploymentPodInitContainerMutator ensures that the deployment's pod's init containers are reconciled
func DeploymentPodInitContainerMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	updated := false

	// Trim excess containers if existing has more than desired
	if len(existing.Spec.Template.Spec.InitContainers) > len(desired.Spec.Template.Spec.InitContainers) {
		existing.Spec.Template.Spec.InitContainers = existing.Spec.Template.Spec.InitContainers[:len(desired.Spec.Template.Spec.InitContainers)]
		updated = true
	}

	// Ensure init containers match
	for i := range desired.Spec.Template.Spec.InitContainers {
		if i >= len(existing.Spec.Template.Spec.InitContainers) {
			// Append missing containers
			existing.Spec.Template.Spec.InitContainers = append(existing.Spec.Template.Spec.InitContainers, desired.Spec.Template.Spec.InitContainers[i])
			updated = true
		} else if !reflect.DeepEqual(existing.Spec.Template.Spec.InitContainers[i], desired.Spec.Template.Spec.InitContainers[i]) {
			// Update mismatched containers
			existing.Spec.Template.Spec.InitContainers[i] = desired.Spec.Template.Spec.InitContainers[i]
			updated = true
		}
	}

	return updated, nil
}

func DeploymentSyncVolumesAndMountsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	changed := false

	// Ensure Volumes slice is initialized
	if existing.Spec.Template.Spec.Volumes == nil {
		existing.Spec.Template.Spec.Volumes = []corev1.Volume{}
	}

	//Add missing Volumes
	for _, desiredVolume := range desired.Spec.Template.Spec.Volumes {
		if !volumeExists(existing.Spec.Template.Spec.Volumes, desiredVolume.Name) {
			existing.Spec.Template.Spec.Volumes = append(existing.Spec.Template.Spec.Volumes, desiredVolume)
			changed = true
		}
	}

	// Sync VolumeMounts for Containers
	for cIdx := range existing.Spec.Template.Spec.Containers {
		updated, newVolumeMounts := syncVolumeMounts(existing.Spec.Template.Spec.Containers[cIdx].VolumeMounts, desired.Spec.Template.Spec.Containers[cIdx].VolumeMounts)
		if updated {
			existing.Spec.Template.Spec.Containers[cIdx].VolumeMounts = newVolumeMounts
			changed = true
		}
	}

	// Sync VolumeMounts for InitContainers
	for cIdx := range existing.Spec.Template.Spec.InitContainers {
		updated, newVolumeMounts := syncVolumeMounts(existing.Spec.Template.Spec.InitContainers[cIdx].VolumeMounts, desired.Spec.Template.Spec.InitContainers[cIdx].VolumeMounts)
		if updated {
			existing.Spec.Template.Spec.InitContainers[cIdx].VolumeMounts = newVolumeMounts
			changed = true
		}
	}

	return changed, nil
}

// Helper function: Check if a volume exists
func volumeExists(volumes []corev1.Volume, name string) bool {
	for _, v := range volumes {
		if v.Name == name {
			return true
		}
	}
	return false
}

// Helper function: Sync Volume Mounts (Add missing)
func syncVolumeMounts(existingMounts, desiredMounts []corev1.VolumeMount) (bool, []corev1.VolumeMount) {
	changed := false
	newVolumeMounts := existingMounts

	// Add missing VolumeMounts from desired
	for _, desiredMount := range desiredMounts {
		if !volumeMountExists(existingMounts, desiredMount.Name) {
			newVolumeMounts = append(newVolumeMounts, desiredMount)
			changed = true
		}
	}

	return changed, newVolumeMounts
}

// Helper function: Check if a volume mount exists
func volumeMountExists(volumeMounts []corev1.VolumeMount, name string) bool {
	for _, vm := range volumeMounts {
		if vm.Name == name {
			return true
		}
	}
	return false
}

func DeploymentRemoveTLSVolumesAndMountsMutator(desired, existing *k8sappsv1.Deployment) (bool, error) {
	// system-database and zync database tls volume mount names in containers and init containers
	volumeNamesToRemove := []string{"writable-tls", "tls-secret"}

	if existing.Spec.Template.Spec.Volumes == nil {
		return false, nil
	}
	volumeModified := false
	// Remove volumes from the deployment spec
	for _, volumeName := range volumeNamesToRemove {
		for idx, volume := range existing.Spec.Template.Spec.Volumes {
			if volume.Name == volumeName {
				// Remove the specified volume
				existing.Spec.Template.Spec.Volumes = append(existing.Spec.Template.Spec.Volumes[:idx], existing.Spec.Template.Spec.Volumes[idx+1:]...)
				volumeModified = true
				break
			}
		}
	}
	// If volumes were removed, ensure volume mounts are also removed from containers
	if volumeModified {
		// For regular containers
		for cIdx, container := range existing.Spec.Template.Spec.Containers {
			for _, volumeName := range volumeNamesToRemove {
				for vIdx, volumeMount := range container.VolumeMounts {
					if volumeMount.Name == volumeName {
						// Remove the volume mount
						container.VolumeMounts = append(container.VolumeMounts[:vIdx], container.VolumeMounts[vIdx+1:]...)
						break
					}
				}
			}
			// Update the container spec with the modified volume mounts
			existing.Spec.Template.Spec.Containers[cIdx] = container
		}
		// For initContainers (if any)
		for cIdx, initContainer := range existing.Spec.Template.Spec.InitContainers {
			for _, volumeName := range volumeNamesToRemove {
				for vIdx, volumeMount := range initContainer.VolumeMounts {
					if volumeMount.Name == volumeName {
						// Remove the volume mount from initContainer
						initContainer.VolumeMounts = append(initContainer.VolumeMounts[:vIdx], initContainer.VolumeMounts[vIdx+1:]...)
						break
					}
				}
			}
			// Update the initContainer spec with the modified volume mounts
			existing.Spec.Template.Spec.InitContainers[cIdx] = initContainer
		}
	}
	// If no modifications were made, return false
	if !volumeModified {
		return false, nil
	}
	return true, nil
}
