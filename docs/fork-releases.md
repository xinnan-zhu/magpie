# Personal SSH releases

[`Fork release`](../.github/workflows/fork-release.yml) builds the personal
`xinnan-zhu/magpie` release entirely in GitHub Actions. No local release
packages or Docker images are needed. Tags are `vX.Y.Z-ssh.N`; the tag must
be on the fork's main and have a completed, successful Test run at its exact
commit. Push the tag after that run, or dispatch the workflow with the existing
tag. A tag pushed too early is refused; rerun the workflow after Test passes.

macOS builds the Apple silicon app and ad-hoc signs it. Linux cross-builds
Windows apps and Windows/Linux terminal binaries in both architectures. Every
binary names this fork's `latest.json` feed. These are personal builds without
Apple notarization or Windows code signing, not the official signed releases.

The publisher generates the feed with SHA-256s, uploads seven binaries/archives
and the feed to a draft, verifies all eight GitHub asset digests, then publishes
Latest. A retry may repair a draft, but never overwrites a published version.
Actions artifacts expire after three days; published release assets remain.
The official release dispatcher is restricted to the official repository.

The inherited Docker workflow builds on GitHub and publishes under the fork's
GHCR namespace; it does not build images on this computer. Desktop/CLI releases
use the Fork release workflow above.
