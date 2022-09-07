/*
Copyright 2021.

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

package controllers

import (
	"errors"
	"hash/fnv"
	"sort"
	"strconv"
	"strings"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/event"
	cachev1alpha1 "weavelab.xyz/cached-certificate-operator/api/v1alpha1"
)

const (
	// maxSecretNameLength defines the max length of a kubernetes secret name
	maxSecretNameLength = 253

	// hashPrefixLength defines the number of chars to keep before each hash
	// hashPrefixLength + len(hash) should not exceed maxSecretNameLength
	hashPrefixLength = 128
)

// ResourceVersionChangesOnly will filter out events that don't change the resource version
type ResourceVersionChangesOnly struct{}

// Create skips all events
func (ResourceVersionChangesOnly) Create(e event.CreateEvent) bool { return false }

// Delete skips all events
func (ResourceVersionChangesOnly) Delete(e event.DeleteEvent) bool { return false }

// Generic skips all events
func (ResourceVersionChangesOnly) Generic(e event.GenericEvent) bool { return false }

// Update will only trigger resyncs on actual updates that change the resource version
func (ResourceVersionChangesOnly) Update(e event.UpdateEvent) bool {
	if e.ObjectOld == nil {
		return false
	}
	if e.ObjectNew == nil {
		return false
	}

	return e.ObjectNew.GetResourceVersion() != e.ObjectOld.GetResourceVersion()
}

func validateSecret(secret *v1.Secret) error {
	if secret == nil {
		return errors.New("secret cannot be nil")
	}

	if _, ok := secret.Data["tls.crt"]; !ok {
		return errors.New("tls.cert not found")
	}

	if _, ok := secret.Data["tls.key"]; !ok {
		return errors.New("tls.key not found")
	}

	// ca.crt may not be required in all cases so it is not checked here

	return nil
}

// getUpstreamCertificateName is used to get a deterministic upstream cert name
// based on the given dns names
func getUpstreamCertificateName(dnsNames ...string) string {
	// this shouldn't be possible for a live cluster because
	// the CRD requires the input dnsNames to have a len > 0
	if len(dnsNames) == 0 {
		return ""
	}

	// copy the input to preserve original order
	names := make([]string, len(dnsNames))
	copy(names, dnsNames)

	// All that matters is the unique list, not the order
	// so we sort the copied slice before processing
	sort.Strings(names)

	resourceName := strings.Join(names, "-")
	resourceName = strings.ReplaceAll(resourceName, "*", "x")

	if len(resourceName) > maxSecretNameLength {
		// the "-3" is to ensure space for the "cc-" prefix
		resourceName = resourceName[:hashPrefixLength-3] + genHash(resourceName)
	}
	resourceName = strings.ReplaceAll(resourceName, "\\", "x")

	return "cc-" + resourceName
}

func genSecretForSync(cachedCert *cachev1alpha1.CachedCertificate, upstreamCert *unstructured.Unstructured, upstreamSecret *v1.Secret) (*v1.Secret, error) {
	if cachedCert == nil {
		return nil, errors.New("a CachedCertificate is required for secret generation")
	}

	if upstreamCert == nil {
		return nil, errors.New("an upstream Certificate is required for secret generation")

	}

	if upstreamSecret == nil {
		return nil, errors.New("an upstream Secret is required for secret generation")

	}

	// create new secret from select parts of the upstream secret
	secret := &v1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cachedCert.Spec.SecretName,
			Namespace:   cachedCert.GetNamespace(),
			Labels:      upstreamSecret.GetLabels(),
			Annotations: upstreamSecret.GetAnnotations(),

			// Contrary to standard `Certificate` resources, CachedCertificate resources *do* mark their secrets
			// to be garbaged collected by k8s. This is because the secret created here is not the source of truth
			// and is just a copy so it does not need to be preserved
			OwnerReferences: []metav1.OwnerReference{
				*metav1.NewControllerRef(cachedCert, cachedCert.GroupVersionKind()),
			},
		},
		Type: upstreamSecret.Type,
		Data: upstreamSecret.Data,
	}

	// Additionaly, we mark the secret with a label and annotation indicating where it came from
	if secret.Labels == nil {
		secret.Labels = map[string]string{}
	}
	secret.Labels[SyncedLabelKey] = "true"

	if secret.Annotations == nil {
		secret.Annotations = map[string]string{}
	}
	secret.Annotations[SourceAnnotationKey] = cachedCert.Namespace + "/" + cachedCert.Name

	return secret, nil
}

func genHash(s string) string {
	hasher := fnv.New64a()
	hasher.Write(([]byte(s)))
	return strconv.FormatUint(hasher.Sum64(), 10)
}

// slicesEqualAfterSort creates copies of the two slices, sorts them and checks for diffs
// it does not use reflect.DeepEqual and thus considers nil and empty slice to be equal
func slicesEqualAfterSort(x, y []string) bool {
	// fast diff for different sizes
	if len(x) != len(y) {
		return false
	}

	// fast diff when a sort is not needed

	if len(x) == 1 {
		return x[0] == y[0]
	}

	xCopy := make([]string, len(x))
	copy(xCopy, x)
	sort.Strings(xCopy)

	yCopy := make([]string, len(y))
	copy(yCopy, y)
	sort.Strings(yCopy)

	for i := range xCopy {
		if yCopy[i] != xCopy[i] {
			return false
		}
	}

	return true
}
