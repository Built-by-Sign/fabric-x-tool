#!/usr/bin/env bash
# issue-biz-cert — one-command TLS certificate issuance for biz components
# against a sign-ca (fabric-ca) server.
#
# Wraps the full ceremony every new component (indexer, trusted-signing, ...)
# otherwise repeats by hand: admin enroll -> register identity -> enroll with
# the tls profile -> collect crt/key plus the CA chain under stable names.
# Idempotent: an already-registered identity is reused (same secret), and the
# cached admin enrollment is reused across runs.
#
# Usage:
#   issue-biz-cert --ca-url http://admin:adminpw@127.0.0.1:7055 \
#                  --name indexer-node [--secret S3cret] \
#                  [--hosts 10.0.0.1,localhost] [--out ./certs]
#
#   --ca-url   fabric-ca URL with the registrar's credentials embedded.
#   --name     identity to register/enroll; also the output file basename.
#   --secret   enrollment secret (default: derived stable value; pass the
#              same one to re-enroll an existing identity).
#   --hosts    comma-separated SANs — set for server certificates, omit for
#              pure client certificates.
#   --out      output directory (default: current directory).
#
# Outputs: <out>/<name>.crt  <out>/<name>.key  <out>/ca-chain.pem
#
# Needs fabric-ca-client on PATH (bundled in the fabric-x-tool image):
#   docker run --rm --network host -u "$(id -u)" -v "$PWD:/out" \
#     ghcr.io/built-by-sign/fabric-x-tool \
#     issue-biz-cert --ca-url ... --name indexer-node --out /out
set -euo pipefail

CA_URL="" NAME="" SECRET="" HOSTS="" OUT="."
while [ $# -gt 0 ]; do
  case "$1" in
    --ca-url) CA_URL="$2"; shift 2 ;;
    --name)   NAME="$2";   shift 2 ;;
    --secret) SECRET="$2"; shift 2 ;;
    --hosts)  HOSTS="$2";  shift 2 ;;
    --out)    OUT="$2";    shift 2 ;;
    -h|--help) sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    *) echo "unknown flag: $1 (see --help)" >&2; exit 2 ;;
  esac
done
[ -n "$CA_URL" ] && [ -n "$NAME" ] || { echo "--ca-url and --name are required (see --help)" >&2; exit 2; }
command -v fabric-ca-client >/dev/null || { echo "fabric-ca-client not on PATH" >&2; exit 1; }

# Default secret: stable per name so a re-run without --secret still matches
# the registered identity. Override with --secret for anything long-lived.
[ -n "$SECRET" ] || SECRET="$(printf '%s' "$NAME" | cksum | cut -d' ' -f1)-biz"

mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
ADMIN_HOME="$OUT/.ca-admin"
ID_HOME="$OUT/.enroll-$NAME"

# CA base URL without the embedded credentials, for the identity enrollment.
BASE_URL="$(printf '%s' "$CA_URL" | sed -E 's#(https?://)[^@/]+@#\1#')"

echo "== admin enrollment =="
if [ -f "$ADMIN_HOME/msp/signcerts/cert.pem" ]; then
  echo "reusing cached registrar enrollment ($ADMIN_HOME)"
else
  fabric-ca-client enroll -u "$CA_URL" -H "$ADMIN_HOME" >/dev/null
fi

echo "== register $NAME =="
if OUTPUT=$(fabric-ca-client register -H "$ADMIN_HOME" \
      --id.name "$NAME" --id.secret "$SECRET" --id.type client 2>&1); then
  echo "registered"
elif printf '%s' "$OUTPUT" | grep -qi "already registered"; then
  echo "already registered — reusing (enrollment will fail if the secret differs)"
else
  printf '%s\n' "$OUTPUT" >&2
  exit 1
fi

echo "== enroll $NAME =="
rm -rf "$ID_HOME"
ENROLL_ARGS=(--enrollment.profile tls)
[ -n "$HOSTS" ] && ENROLL_ARGS+=(--csr.hosts "$HOSTS")
ENROLL_URL="$(printf '%s' "$BASE_URL" | sed -E "s#(https?://)#\1$NAME:$SECRET@#")"
fabric-ca-client enroll -u "$ENROLL_URL" -H "$ID_HOME" "${ENROLL_ARGS[@]}" >/dev/null

cp "$ID_HOME/msp/signcerts/cert.pem" "$OUT/$NAME.crt"
cp "$ID_HOME"/msp/keystore/*_sk "$OUT/$NAME.key"
chmod 600 "$OUT/$NAME.key"
cat "$ADMIN_HOME"/msp/cacerts/*.pem "$ADMIN_HOME"/msp/intermediatecerts/*.pem \
  > "$OUT/ca-chain.pem" 2>/dev/null || cat "$ADMIN_HOME"/msp/cacerts/*.pem > "$OUT/ca-chain.pem"

echo "== done =="
openssl x509 -in "$OUT/$NAME.crt" -noout -subject 2>/dev/null || true
[ -n "$HOSTS" ] && openssl x509 -in "$OUT/$NAME.crt" -noout -ext subjectAltName 2>/dev/null || true
echo "files: $OUT/$NAME.crt $OUT/$NAME.key $OUT/ca-chain.pem"
