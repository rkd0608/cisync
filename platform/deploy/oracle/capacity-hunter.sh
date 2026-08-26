#!/usr/bin/env bash
# A1 capacity hunter — retries instance launch across all ADs until Oracle frees space.
set -euo pipefail
COMP="ocid1.tenancy.oc1..aaaaaaaa3tskfwx5umodtl34xr5k5sagn25c5bi3pmv74c5zemehx2dlwuuq"
OCPUS="${OCPUS:-2}"; MEM="${MEM:-12}"
REPO_TOKEN="${REPO_TOKEN:?export REPO_TOKEN}"
INIT="$HOME/Desktop/cisync-cloud-init.txt"
[[ $OCPUS == 4 ]] && INIT="$HOME/Desktop/cisync-cloud-init.txt"
VCN_ID=$(oci network vcn list --compartment-id "$COMP" 2>/dev/null | jq -r '.data[] | select(."display-name"=="cisync") | .id' | tail -1)
SUBNET_ID="ocid1.subnet.oc1.us-chicago-1.aaaaaaaa27onh7j5h4kbmllb62f4cybqoquxoucn4sfmyiwnvadk36rgqtja"
IMAGE_ID=$(oci compute image list --compartment-id "$COMP" --operating-system "Canonical Ubuntu" --operating-system-version "24.04" --shape "VM.Standard.A1.Flex" --sort-by TIMECREATED 2>/dev/null | jq -r '.data[0].id')
ADS=$(oci iam availability-domain list --compartment-id "$COMP" 2>/dev/null | jq -r '.data[].name')
echo "subnet=$SUBNET_ID image=$IMAGE_ID"; [ -s "$SUBNET_ID" ] || exit 1
for attempt in $(seq 1 72); do
  for AD in $ADS; do
    echo "[attempt $attempt] launching in $AD (${OCPUS}/${MEM})..."
    OUT=$(oci compute instance launch \
      --availability-domain "$AD" --compartment-id "$COMP" --image-id "$IMAGE_ID" \
      --shape VM.Standard.A1.Flex --shape-config "{\"ocpus\":$OCPUS,\"memoryInGBs\":$MEM}" \
      --subnet-id "$SUBNET_ID" --assign-public-ip true --display-name cisync-p1 \
      --user-data-file "$INIT" 2>&1) && {
      ID=$(echo "$OUT" | jq -r '."data"."id" // .id')
      echo "LAUNCHED: $ID — waiting for RUNNING..."
      oci compute instance update --instance-id "$ID" --wait-for-state RUNNING >/dev/null 2>&1 || true
      IP=$(oci compute instance list-vnics --compartment-id "$COMP" --instance-id "$ID" 2>/dev/null | jq -r '."data"[0]."public-ip"')
      echo "PUBLIC IP: $IP"; echo "DONE — give this IP to the integrator."; exit 0
    }
    if echo "$OUT" | grep -q "Out of host capacity"; then
      echo "  no capacity in $AD"; sleep 15
    else
      echo "UNEXPECTED ERROR:"; echo "$OUT" | tail -12; exit 2
    fi
  done
  echo "--- all ADs full; sleeping 10 min ---"; sleep 600
done
echo "exhausted 12h of retries"; exit 3
