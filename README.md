# proglog

『Go言語による分散サービス』の勉強記録

## 起動前の準備

- Docker
- Kind
- Kubectl
    - Krew
        - relay
- Helm

## 起動のために使うもの

```
make build-docker
kind load docker-image github.com/2222-42/proglog:0.0.1
helm install proglog deploy/proglog
```

```
kubectl relay host/proglog-0.proglog.default.svc.cluster.local 8400
```


## acknowledgements

`deploy/metacontroller/templates/metacontroller.yaml` のコードに関しては、metacontroller/metacontrolelrの [`metacontroller-crds-v1.yaml`](https://github.com/metacontroller/metacontroller/blob/master/manifests/production/metacontroller-crds-v1.yaml)を用いています。
