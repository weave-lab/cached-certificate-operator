# cached-certificate-operator

### CachedCertificate Workflow

When a `CachedCertificate` is created or updated the operator does the following:

* Check for a valid upstream `Certificate`
  * Create if missing and then resync
* Wait for upstream `Secret` to be created
* Sync the upstream `Secret` to the target local secret name
* Watch for upstream `Secret` changes and sync down

### Quickstart Install

The process below uses the kustomize files in `./config` to enable easy deployment.

```bash
# get the latest code
git clone git@github.com:weave-lab/cached-certificate-operator.git
cd cached-certificate-operator

# install operator into the K8s cluster specified in ~/.kube/config
kubectl apply -k config/default
```

### Try out the operator with a self-signed ca

The steps below depend on having cert-manager installed in the cluster.

We do not cover installing `cert-manager`. Instead see the [official cert-manager installation docs](https://cert-manager.io/docs/installation/).

#### Create a selfSigned issuer

```bash
# wait for cert-manager to come up
kubectl create -f <(cat <<EOF
---
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: selfsigned-issuer
spec:
  selfSigned: {}
EOF
)
```

#### Put some basic certs in

```bash
kubectl apply -f config/samples/cache_v1alpha1_cachedcertificate.yaml
kubectl apply -f config/samples/cache_v1alpha1_cachedcertificate-alt.yaml
```

You should see two valid secrets for the 2 resources fairly quickly:

```bash
kubectl get secrets -l cache.weavelab.xyz/synced-from-cache
```

### Create secondary `CachedCertificates` for DNSNames that have already had certs provisioned

```bash
kubectl apply -f config/samples/cache_v1alpha1_cachedcertificate-2.yaml
kubectl apply -f config/samples/cache_v1alpha1_cachedcertificate-alt-2.yaml
```

You should see 4 valid secrets for the 4 resources.

```bash
kubectl get secrets -l cache.weavelab.xyz/synced-from-cache
```

However, if you check for `Certificates`, you will only see two resources. This is because even though we have 4 total `CachedCertificates` there are only two unique sets of `dnsNames` so the operator
prevents duplicates from being created.

```bash
kubectl get certificates -n cached-certificate-operator-system
```

### Local Development

#### Create a test kubernetes cluster

The official docs use k3d but any cluster creation tool will work.

```bash
k3d cluster create cc-op
```

> NOTE: Be **absolutely** sure this is done and that your current `kubectl` context is for your temp cluster before continuing

#### Install the CRDs

```bash
make install
```

#### Install the latest cert-manager

This is a bare minimum install with default configuration for cert-manager. It is most likely not ideal for production use but works just fine for local development.

```bash
kubectl create -f https://github.com/jetstack/cert-manager/releases/download/v1.4.0/cert-manager.yaml
```

#### Run the operator locally

```bash
make run
```

Next try the operator by following the instrutctions in [](#Try out the operator with a self-signed ca)

#### Testing

This operator has both standard unit tests and full-featured integration tests.

All tests can be done using `make test`

You can also manually install `kubebuilder` and it's dependencies which will allow you to run a full `go test ./...` locally or even run tests via your editor!

##### Setup for test exec without using `make`

```bash
K8S_VERSION=1.19.2

sudo mkdir -p /usr/local/kubebuilder

# Get the latest kubebuilder and put it into the expected location
curl -L -o kubebuilder https://go.kubebuilder.io/dl/latest/$(go env GOOS)/$(go env GOARCH)
chmod +x kubebuilder && mv kubebuilder /usr/local/kubebuilder/bin/

# Get full k8s envtest deps and putthem into the expected locatoin
curl -sSLo envtest-bins.tar.gz "https://storage.googleapis.com/kubebuilder-tools/kubebuilder-tools-${K8S_VERSION}-$(go env GOOS)-$(go env GOARCH).tar.gz"
sudo tar -C /usr/local/kubebuilder/ --strip-components=1 -zvxf envtest-bins.tar.gz

# Add kubebuilder to your path
echo 'export PATH=$PATH:/usr/local/kubebuilder/bin' >> ~/.bashrc
. ~/.bashrc
```

Now `go test ./...` should work!
