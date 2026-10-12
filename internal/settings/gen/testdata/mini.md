# All settings

<span id="scopes" />

## Settings index

Every key below links to its entry.

<ReferenceFilter
  noun="settings"
/>

| Key | Description | Topic | Scope |
| :- | :- | :- | :- |
| [`alpha`](#alpha) | Pick the [alpha mode](/docs/en/alpha) for `--alpha` runs | Model and responses | Any file |
| [`beta`](#beta) | Choose a beta level \| or none | Model and responses | User or managed |
| [`oldKey`](#oldkey) | Removed in v9.9.9 | Memory and context | Any file |
| [`sandbox.network.allowedDomains`](#sandbox-network-alloweddomains) | Pre-allow domains | Sandbox settings | Managed |
| [`hooks`](#hooks) | Run commands on events | Hooks and automation | Global config |

## Model and responses

### `alpha`

Prose about alpha with a decoy below.

```json settings.json
* **Scope**: [`Managed`](#scopes)
* **Type**: decoy
```

* **Scope**: [`Any file`](#scopes)
* **Type**: Boolean
  * `true`: alpha is on
  * `false`: alpha is off, see [the docs](/docs/en/alpha)
* **Default**: `false`
* **Per-session overrides**: [`--alpha-mode`](/docs/en/cli) takes precedence over [`ALPHA_MODE`](/docs/en/env-vars), and `--alpha-mode` again or `ALPHA_MODE=1` change nothing
* **Type**: second Type bullet is ignored

### `beta`

* **Scope**: [`User or managed`](#scopes). A repository can't set it for you.
* **Type**: string, one of:

  * `"low"`: less
  * `"high"`: more
* **Default**: `"low"`

#### Fields for `beta`

* **Type**: not a key bullet

## Memory and context

### `oldKey`

<Warning>
  Removed in v9.9.9, together with the old tool. Setting it has no effect.
</Warning>

Through v9.9.8 you set this key.

### `sandbox.network.allowedDomains`

~~~
### `notAKey`
~~~

* **Scope**: [`Managed`](#scopes)
* **Type**: array of strings, each a domain
* **Default**: unset

### `hooks`

* **Scope**: [`Global config`](#scopes)
* **Type**: object keyed by [hook event](/docs/en/hooks); each value is an array
* **Default**: unset &amp; empty
