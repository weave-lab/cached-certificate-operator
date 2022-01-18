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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CachedCertificateSpec defines the desired state of CachedCertificate
type CachedCertificateSpec struct {
	// SecretName indicates the name of the secret which will be created once the upstream certificate has been generated
	// Changing this field *will not* cause a new upstream certificate to be created
	// If changed, old secrets will not get cleaned up by the operator
	//
	// It is optional and will be defaulted to the CachedCertificate Name
	SecretName string `json:"secretName,omitempty"`

	// IssuerRef identifies a single issuer to use when generating the cert
	// Changing this field may cause a new upstream certificate to be created in the cache namespace
	IssuerRef IssuerRef `json:"issuerRef"`

	//+kubebuilder:validation:MinItems=1
	// DNSNames is a list of unique dns names for the cert
	// Changing this field may cause a new upstream certificate to be created in the cache namespace
	DNSNames []string `json:"dnsNames"`
}

// IssuerRef points to a CertManger issuer
type IssuerRef struct {
	// Name is the name of the issuer
	Name string `json:"name"`

	// Kind indicates the issuer kind to use
	Kind string `json:"kind"`

	// Group is the name of the issuer group. Optional
	Group string `json:"group,omitempty"`
}

// CachedCertificateStatus defines the observed state of CachedCertificate
type CachedCertificateStatus struct {
	UpstreamReady bool                   `json:"upstreamReady"`
	UpstreamRef   *ObjectReference       `json:"upstreamRef,omitempty"`
	State         CachedCertificateState `json:"state"`
}

type CachedCertificateState string

const (
	CachedCertificateStatePending CachedCertificateState = "Pending"
	CachedCertificateStateSynced  CachedCertificateState = "Synced"
	CachedCertificateStateError   CachedCertificateState = "Error"
)

// ObjectReference is a reference to an object with a given name and Namespace
type ObjectReference struct {
	// Name of the resource being referred to.
	Name string `json:"name"`

	// Namespace of the resource being referred to.
	Namespace string `json:"namespace"`
}

//+kubebuilder:object:root=true
//+kubebuilder:subresource:status
//+kubebuilder:printcolumn:name="Upstream_Ready",type=string,JSONPath=`.status.upstreamReady`
//+kubebuilder:printcolumn:name="State",type=string,JSONPath=`.status.state`

// CachedCertificate is the Schema for the cachedcertificates API
type CachedCertificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CachedCertificateSpec   `json:"spec,omitempty"`
	Status CachedCertificateStatus `json:"status,omitempty"`
}

//+kubebuilder:object:root=true

// CachedCertificateList contains a list of CachedCertificate
type CachedCertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CachedCertificate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&CachedCertificate{}, &CachedCertificateList{})
}
