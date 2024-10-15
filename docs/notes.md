## Create a cluster locally
```
kind create cluster
```
## Install localstack in the cluster
```
helm repo add localstack-repo https://helm.localstack.cloud
helm upgrade --install localstack localstack-repo/localstack
helm repo add localstack-charts https://localstack.github.io/helm-charts

kubectl create namespace localstack
helm install localstack localstack/localstack --namespace localstack -f values.yaml
kubectl get pods -n localstack

kubectl port-forward svc/localstack -n localstack 4566:4566
```
## Set environment variables for access to localstack
```
export AWS_ACCESS_KEY_ID=test
export AWS_SECRET_ACCESS_KEY=test
export AWS_DEFAULT_REGION=us-east-1
export AWS_ENDPOINT_URL=http://localhost:4566

# Testing
aws --endpoint-url=http://localhost:4566 s3api create-bucket --bucket my-bucket-name --region us-east-1
aws --endpoint-url=http://localhost:4566 s3 ls
```
## Initialize a new project
```
kubebuilder init --domain kb.dev --repo github.com/jiaqi-yin/localstack-controller
```
## Scaffold a Kubernetes API
```
kubebuilder create api \
--kind S3Bucket \
--group localstack \
--version v1 \
--resource true \
--controller true
```
## Install CRDs into the K8s cluster
```
make install
```
## Apply a configuration to a S3Bucket resource 
```
kubectl apply -f config/samples/localstack_v1_s3bucket.yaml
```
## Cleanup
```
helm uninstall localstack --namespace localstack
kubectl delete namespace localstack
```