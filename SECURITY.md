# Security policy

## Supported versions

Līdza is before 1.0. Security fixes go into the next release; only the
newest release is supported, and older ones are not patched.

| Version            | Supported |
| ------------------ | --------- |
| newest 0.x release | yes       |
| older releases     | no        |

`lidza update` moves the CLI and an app's module to the newest release.
`CHANGELOG.md` lists what changed in each one.

`docs/security.md` describes what Līdza defends against and what it
leaves to the app.

## Reporting a vulnerability

Report privately through GitHub: the repository's **Security** tab,
then **Report a vulnerability**
(https://github.com/agim/lidza/security/advisories/new). Do not open a
public issue or pull request for a vulnerability.

Include what you can of:

- the affected part (a pack, the CLI, the router, generated code, a
  template) and the release;
- steps or a small app that reproduces it;
- the impact you see: what an attacker can read, change or run.

## What to expect

- An acknowledgement within 3 working days.
- A first assessment within 7 days: whether it is accepted, and its
  severity.
- For an accepted report, a fix in a release as soon as it is ready,
  then a GitHub security advisory that credits you unless you ask not to
  be named. You see the fix before it is public.
- For a declined report, the reason.

Please keep the report private until the advisory is published, or
until 90 days have passed, whichever comes first.

## Scope

In scope: this repository, meaning the framework, its official packs
(auth, credentials, storage, mail, the admin pages and the others), the
`lidza` CLI and its MCP server, the code it generates, and the app
templates.

Out of scope:

- apps built with Līdza, unless the flaw comes from the framework or
  its generated code;
- third-party services the packs talk to;
- findings that need an attacker who already controls the server, the
  master key or the developer's machine.
