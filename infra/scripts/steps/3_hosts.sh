cat <<EOF | kubectl apply -f -
apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  name: default
  namespace: metallb-system
spec:
  addresses:
  - 192.168.1.240-192.168.1.250
  - 91.132.57.227/32
  autoAssign: true
  avoidBuggyIPs: false
EOF


kubectl annotate service traefik -n traefik metallb.universe.tf/loadBalancerIPs=91.132.57.227 --overwrite

kubectl patch ingress -n minio-operator minio-operator-console --type='json' -p='[{"op": "add", "path": "/metadata/annotations/cert-manager.io~1cluster-issuer", "value": "letsencrypt"}]'