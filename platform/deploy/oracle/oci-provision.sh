#!/usr/bin/env bash
# oci-provision.sh — one-shot Phase-1 deployment onto Oracle Always-Free.
# Creates VCN+subnet+security-list+A1 instance, then cloud-init installs
# docker, clones the repo, fills .env.prod skeleton, and boots the stack.
#
# Prereqs: OCI CLI configured (`oci setup config`), an API auth token,
# compartment OCID exported. Usage:
#   export OCI_COMPARTMENT_OCID=ocid1.compartment.oc1..
#   ./platform/deploy/oracle/oci-provision.sh [availability-domain-index]
set -euo pipefail

# Private repo? Export REPO_TOKEN with a fine-grained PAT (Contents:Read).
# WHY token-in-URL: simplest unattended clone on a fresh box; rotate or
# revoke the token post-deploy if preferred (git remote stays configured).
REPO_URL="${REPO_URL:-https://github.com/rkd0608/sauron.git}"
REPO_TOKEN="${REPO_TOKEN:-}"
if [ -n "$REPO_TOKEN" ]; then
  REPO_URL="https://x-access-token:${REPO_TOKEN}@github.com/rkd0608/sauron.git"
fi
SHAPE="${SHAPE:-VM.Standard.A1.Flex}"
OCPUS="${OCPUS:-4}"
MEMORY_GB="${MEMORY_GB:-24}"
IMAGE_OS="Canonical Ubuntu"
IMAGE_VER="24.04"
AD_INDEX="${1:-1}"

command -v oci >/dev/null || { echo "install OCI CLI: brew install oci-cli"; exit 1; }
: "${OCI_COMPARTMENT_OCID:?export OCI_COMPARTMENT_OCID=ocid1.compartment.oc1..}"

HERE="$(cd "$(dirname "$0")" && pwd)"
COMP="$OCI_COMPARTMENT_OCID"

echo "==> resolving availability domain #$AD_INDEX"
AD=$(oci iam availability-domain list --compartment-id "$COMP" \
  --query "data[$((AD_INDEX-1))].name" --raw-output)
echo "    $AD"

echo "==> listing Ubuntu ${IMAGE_VER} aarch64 image"
IMG=$(oci compute image list --compartment-id "$COMP" \
  --operating-system "$IMAGE_OS" --operating-system-version "$IMAGE_VER" \
  --shape "$SHAPE" --sort-by TIMECREATED \
  --query 'data[0].id' --raw-output)

echo "==> rendering cloud-init"
sed "s|\${REPO_URL}|$REPO_URL|" "$HERE/cloud-init.yaml" > /tmp/sauron-cloud-init.yaml

echo "==> VCN + networking"
VCN=$(oci network vcn create --compartment-id "$COMP" --cidr-blocks '["10.0.0.0/16"]' \
  --display-name sauron-vcn --wait-for-state AVAILABLE --query data.id --raw-output)
SUB=$(oci network subnet create --compartment-id "$COMP" --vcn-id "$VCN" \
  --cidr-block "10.0.1.0/24" --display-name sauron-sub --wait-for-state AVAILABLE \
  --query data.id --raw-output)
IGW=$(oci network internet-gateway create --compartment-id "$COMP" --vcn-id "$VCN" \
  --enabled true --display-name sauron-igw --query data.id --raw-output)
oci network route-table update --rt-id "$(oci network vcn get --vcn-id "$VCN" \
  --query data.default_route_table_id --raw-output)" \
  --route-rules "[{\"destination\":\"0.0.0.0/0\",\"networkEntityId\":\"$IGW\"}]" >/dev/null
SL=$(oci network vcn get --vcn-id "$VCN" --query data.default_security_list_id --raw-output)
oci network security-list update --security-list-id "$SL" --security-list-ingress-security-rules '[
  {"protocol":"6","source":"0.0.0.0/0","tcpOptions":{"destinationPortRange":{"min":22,"max":22}}},
  {"protocol":"6","source":"0.0.0.0/0","tcpOptions":{"destinationPortRange":{"min":80,"max":80}}},
  {"protocol":"6","source":"0.0.0.0/0","tcpOptions":{"destinationPortRange":{"min":443,"max":443}}}
]' >/dev/null
echo "    vcn=$VCN"

echo "==> launching $SHAPE (${OCPUS} OCPU / ${MEMORY_GB}GB) — capacity lottery possible"
INSTANCE=$(oci compute instance launch \
  --availability-domain "$AD" --compartment-id "$COMP" --image-id "$IMG" \
  --shape "$SHAPE" --shape-config '{"ocpus":'"$OCPUS"',"memoryInGBs":'"$MEMORY_GB"'}' \
  --subnet-id "$SUB" --assign-public-ip true --display-name sauron-p1 \
  --user-data-file "/tmp/sauron-cloud-init.yaml" \
  --wait-for-state RUNNING --query data.id --raw-output)
IP=$(oci compute instance list-vnics --compartment-id "$COMP" --instance-id "$INSTANCE" \
  --query 'data[0]."public-ip"' --raw-output)

echo "==> instance $INSTANCE public-ip $IP"
cat <<EOF

NEXT STEPS (cloud-init runs ~4 min after boot):
  ssh ubuntu@$IP                          # key = your default OCI ssh key
  tail -f /var/log/cloud-init-output.log  # watch docker install + stack boot
  open https://<your-dns>/ once Caddy obtains a certificate
DNS: point an A record at $IP (required for GitHub webhooks).
EOF
