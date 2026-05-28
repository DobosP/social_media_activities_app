"""EUDI Wallet age verification over OpenID4VP - the cryptographic core.

- ``verifier`` checks the presented age attestation's ES256 signature against a trusted
  issuer, binds it to our audience + nonce (replay protection) and expiry, and returns the
  verified claims.
- ``trust`` resolves which issuers are trusted (the EU trust list in production; a local
  test issuer in sandbox mode).
- ``issuer`` is a sandbox credential issuer used by the demo/API flow and the tests so the
  whole thing is exercisable without the (not-yet-live) national wallet.
"""
