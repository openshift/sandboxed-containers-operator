# Deploy KBS

## Setup KBS

Create and configure the namespace for KBS deployment:
```
oc new-project coco-kbs

# Allow anyuid in kbs pod
oc adm policy add-scc-to-user anyuid -z default -n coco-kbs
```

Create the KBS configmap:
```
oc apply -f kbs-cm.yaml
```

Create certificate for KBS:
```
openssl genpkey -algorithm ed25519 >kbs.key
openssl pkey -in kbs.key -pubout -out kbs.pem

# Create a secret object from the kbs.pem file.
oc create secret generic kbs-auth-public-key --from-file=kbs.pem -n coco-kbs
```

Create secrets that will be sent to the application by the KBS:
```
# Create an application secret
head -c 32 /dev/urandom | openssl enc > key.bin

# Create a secret object from the user key file (key.bin).
oc create secret generic kbs-keys --from-file=key.bin
```

Deploy KBS:
```
oc apply -f kbs-deploy.yaml
```

## Setup cloud-api-adatpor
Get KBS route:
```
KBS_RT=$(oc get routes -n coco-kbs -ojsonpath='{range .items[*]}{.spec.host}{"\n"}{end}')
echo ${KBS_RT}
```

Update peer-pods-cm ConfigMap with KBS route:
```
export AA_KBC_PARAMS=cc_kbc::http://${KBS_RT}
oc get cm/peer-pods-cm -n openshift-sandboxed-containers-operator -o json | jq --arg AA_KBC_PARAMS "$AA_KBC_PARAMS" '.data.AA_KBC_PARAMS = $AA_KBC_PARAMS' | kubectl replace -f -
```

Restart cloud-api-adaptor to be updated with the CM change:
```
kubectl set env ds/peerpodconfig-ctrl-caa-daemon -n openshift-sandboxed-containers-operator REBOOT="$(date)"
```

## Retrieving the keys from KBS from confidential container
```
wget  http://127.0.0.1:8006/cdh/resource/mysecret/workload-keys/key.bin
```
