# Production-oriented Charters

These examples are deliberately fail-closed and are validated with
`runeward validate --strict` in CI. They use a read-only root filesystem,
explicit network allowlists, explicit tool rules, finite budgets, and Chronicle
redaction.

They are secure starting points, not universal production policy. Before use,
pin every container image by digest, narrow the allowed hostnames and commands
for your workload, and use Kubernetes strict egress (`enforce = "strict"`) or
another kernel-enforced network boundary when cooperative proxy enforcement is
not sufficient.

The examples in the parent directory remain tutorials and feature demos. They
may intentionally trade strictness for approachability.
