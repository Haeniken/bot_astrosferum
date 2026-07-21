# Security policy

## Supported versions

Security fixes target the current default branch until versioned releases are published.

## Reporting a vulnerability

Do not open a public issue containing an exploitable vulnerability, token, credential, user identifier, private coordinate, or database dump. Use GitHub's private vulnerability-reporting feature for this repository. Include affected revision, impact, reproduction steps with synthetic data, and a proposed mitigation if available.

If a secret is exposed, revoke or rotate it before reporting. Removing it from the latest commit is not sufficient because Git history and external caches may retain it.

## Deployment baseline

Keep runtime secrets outside Git, bind services only as required, run the container without root and with a read-only root filesystem, update dependencies and base images, restrict access to PostgreSQL and bind volumes, and back up only the data that the operator is authorized to retain.
