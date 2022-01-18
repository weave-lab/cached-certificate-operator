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
	"strings"
	"testing"

	"github.com/go-test/deep"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	cachev1alpha1 "weavelab.xyz/cached-certificate-operator/api/v1alpha1"
)

func Test_getUpstreamName(t *testing.T) {
	type args struct {
		dnsNames []string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{}, // empty in, empty out
		{
			"single",
			args{[]string{"test.example.com"}},
			"cc-test.example.com",
		},
		{
			"multiple",
			args{[]string{"test.example.com", "secondary.example.com"}},
			"cc-secondary.example.com-test.example.com", // sort should have happened
		},
		{
			"long is hashed",
			args{[]string{
				"a.example.com",
				strings.Repeat("b", 63) + ".example.com",
				strings.Repeat("c", 63) + ".example.com",
				strings.Repeat("d", 63) + ".example.com",
				strings.Repeat("f", 63) + ".example.com",
			}},
			"cc-a.example.com-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.example.com-ccccccccccccccccccccccccccccccccccc12004226272052881208",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := getUpstreamCertificateName(tt.args.dnsNames...); got != tt.want {
				t.Errorf("getUpstreamName() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_getUpstreamNameSort(t *testing.T) {
	dnsNames := []string{"b", "a", "c"}

	// call the func
	getUpstreamCertificateName(dnsNames...)

	// order of the referenced slice should not be altered
	if dnsNames[0] != "b" {
		t.Error("getUpstreamCertificateName sorted the source slice")
	}
}

func Test_secretIsValid(t *testing.T) {
	type args struct {
		secret *v1.Secret
	}
	tests := []struct {
		name    string
		args    args
		wantErr bool
	}{
		{}, // nil is invalid
		{
			"missing all data",
			args{
				&v1.Secret{},
			},
			false,
		},
		{
			"missing key",
			args{
				&v1.Secret{
					Data: map[string][]byte{"tls.crt": nil},
				},
			},
			false,
		},
		{
			"missing cert",
			args{
				&v1.Secret{
					Data: map[string][]byte{"tls.crt": nil},
				},
			},
			false,
		},
		{
			"valid",
			args{
				&v1.Secret{
					Data: map[string][]byte{"tls.crt": nil, "tls.key": nil},
				},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validateSecret(tt.args.secret); (got == nil) != tt.wantErr {
				t.Errorf("secretIsValid() = unexpected err %v", got)
			}
		})
	}
}

func Test_getNewSecret(t *testing.T) {
	type args struct {
		cachedCert     *cachev1alpha1.CachedCertificate
		upstreamCert   *unstructured.Unstructured
		upstreamSecret *v1.Secret
	}
	tests := []struct {
		name    string
		args    args
		want    *v1.Secret
		wantErr bool
	}{
		{
			"empty invalid",
			args{},
			nil,
			true,
		},
		{
			"missing cachedCert invalid",
			args{
				nil,
				&unstructured.Unstructured{},
				&v1.Secret{},
			},
			nil,
			true,
		},
		{
			"missing upstreamCert",
			args{
				&cachev1alpha1.CachedCertificate{},
				nil,
				&v1.Secret{},
			},
			nil,
			true,
		},
		{
			"missing upstreamSecret",
			args{
				&cachev1alpha1.CachedCertificate{},
				&unstructured.Unstructured{},
				nil,
			},
			nil,
			true,
		},
		{
			"valid",
			args{
				&cachev1alpha1.CachedCertificate{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "cached-cert-name",
						Namespace: "cached-cert-namespace",
					},
					Spec: cachev1alpha1.CachedCertificateSpec{
						SecretName: "cached-cert-secret-name",
					},
				},
				&unstructured.Unstructured{},
				&v1.Secret{},
			},
			&v1.Secret{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "cached-cert-secret-name",
					Namespace: "cached-cert-namespace",
					Labels: map[string]string{
						SyncedLabelKey: "true",
					},
					OwnerReferences: []metav1.OwnerReference{{
						Name:               "cached-cert-name",
						Controller:         boolP(true),
						BlockOwnerDeletion: boolP(true),
					}},
					Annotations: map[string]string{
						SourceAnnotationKey: "cached-cert-namespace/cached-cert-name",
					},
				},
			},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := genSecretForSync(tt.args.cachedCert, tt.args.upstreamCert, tt.args.upstreamSecret)
			if (err != nil) != tt.wantErr {
				t.Errorf("genSecretForSync() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			for _, diff := range deep.Equal(got, tt.want) {
				t.Errorf("genSecretForSync() diff %v", diff)
			}
		})
	}
}

func boolP(b bool) *bool {
	return &b
}

func Test_genHash(t *testing.T) {
	type args struct {
		s string
	}
	tests := []struct {
		name string
		args args
		want string
	}{
		{
			"hash value",
			args{"hash"},
			"3331993900282443793",
		},
		{
			"hash2 has different value",
			args{"hash2"},
			"12478621798616408953",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := genHash(tt.args.s); got != tt.want {
				t.Errorf("genHash() = %v, want %v", got, tt.want)
			}
		})
	}
}

func Test_slicesEqualAfterSort(t *testing.T) {
	type args struct {
		x []string
		y []string
	}
	tests := []struct {
		name string
		args args
		want bool
	}{
		{
			"not equal",
			args{
				[]string{"x"},
				[]string{},
			},
			false,
		},
		{
			"empty and nil count as equal",
			args{
				nil,
				[]string{},
			},
			true,
		},
		{
			"one item equal",
			args{
				[]string{"x"},
				[]string{"x"},
			},
			true,
		},
		{
			"equal after sort",
			args{
				[]string{"y", "x"},
				[]string{"x", "y"},
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := slicesEqualAfterSort(tt.args.x, tt.args.y); got != tt.want {
				t.Errorf("slicesEqualAfterSort() = %v, want %v", got, tt.want)
			}

			// reverse params and ensure same result
			if got := slicesEqualAfterSort(tt.args.y, tt.args.x); got != tt.want {
				t.Errorf("slicesEqualAfterSort() revered params - got = %v, want %v", got, tt.want)
			}
		})
	}
}
