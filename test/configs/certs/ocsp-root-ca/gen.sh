#!/usr/bin/env bash
set -euo pipefail

# gen.sh generates certificates used in OCSP tests. It generates a CA, and intermediate CA,
# client certs, and a few different types of server certs with different OCSP
# settings. This requires OpenSSL, not LibreSSL.
#
# usage: ./gen.sh

################################################################################
# Setup Root CA
################################################################################
mkdir -p ./rootCA/newcerts ./rootCA/certs ./rootCA/crl ./rootCA/newcerts ./rootCA/private
rm -f rootCA/index.txt
touch rootCA/index.txt
echo "01" > rootCA/serial

openssl_bin="/opt/homebrew/opt/openssl@3/bin/openssl"

# Create root CA key.
$openssl_bin genrsa -out ./rootCA/private/ca.key.pem

# Create root certificate.
$openssl_bin req --batch -config openssl.cnf \
      -key ./rootCA/private/ca.key.pem \
      -new -x509 -days 7300 -sha256 -extensions v3_ca \
      -out ./rootCA/certs/ca.cert.pem

################################################################################
# Setup Intermediate CA
################################################################################
mkdir -p ./interCA/newcerts ./interCA/certs ./interCA/crl ./interCA/newcerts ./interCA/private ./interCA/csr
rm -f interCA/index.txt
touch interCA/index.txt
echo "01" > interCA/serial

# Create intermediate CA key.
$openssl_bin genrsa -out ./interCA/private/intermediate.key.pem

# Create intermediate CA cert CSR.
$openssl_bin req --batch -config intermediate.cnf -new -sha256 \
      -key ./interCA/private/intermediate.key.pem \
      -out ./interCA/csr/intermediate.csr.pem

# Create the intermediate CA cert.
$openssl_bin ca -config openssl.cnf -extensions v3_intermediate_ca \
      -days 3650 -notext -md sha256 \
      -in ./interCA/csr/intermediate.csr.pem \
      -out ./interCA/certs/intermediate.cert.pem

################################################################################
# Create the CA cert chain.
################################################################################

cat interCA/certs/intermediate.cert.pem rootCA/certs/ca.cert.pem > interCA/certs/ca-chain.cert.pem

################################################################################
# Create the server certs.
################################################################################

$openssl_bin genrsa -out ./interCA/private/server.key.pem

$openssl_bin req -config intermediate.cnf \
      -key ./interCA/private/server.key.pem \
      -new -sha256 -out ./interCA/csr/server.csr.pem

$openssl_bin ca -config intermediate.cnf \
      -extensions server_cert -days 375 -notext -md sha256 \
      -in ./interCA/csr/server.csr.pem \
      -out ./interCA/certs/server.cert.pem
