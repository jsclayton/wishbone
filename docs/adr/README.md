# Architecture decision records

An ADR records a decision that is hard to reverse, and why it was made. Smaller choices are in [`docs/rationale.md`](../rationale.md). Each ADR has a `status` in its front matter. Read the ADRs that touch your area before you change it.

| ADR | Decision |
|---|---|
| [0001](0001-login-links-and-passkeys-instead-of-oidc.md) | Login links and passkeys instead of OIDC |
| [0005](0005-store-links-sit-under-an-item.md) | Store links sit under an item instead of being composite items |
| [0006](0006-parents-see-and-edit-their-childrens-lists.md) | Parents see and edit their children's lists |
| [0007](0007-one-address-per-resource-shaped-by-the-caller.md) | One address per resource, shaped by the caller |
| [0008](0008-one-admin-set-by-the-cli.md) | One admin, set by the CLI |
| [0009](0009-lists-have-equal-recipients-not-an-owner.md) | Lists have equal recipients, not an owner |
| [0010](0010-lists-are-visible-through-their-recipients.md) | Lists are visible through their recipients |
| [0011](0011-a-gift-is-one-record-from-claim-to-received.md) | A gift is one record from claim to Received |
| [0012](0012-migrate-in-place-with-an-automatic-backup.md) | Migrate in place, with an automatic backup |
| [0013](0013-native-apps-redeem-a-login-link-for-a-bearer-session.md) | Native apps redeem a login link for a bearer session |

## Numbers

Numbers 0002 to 0004 are not used. Those decisions were replaced by later ones, and a replaced ADR is removed, not kept. A number is never used twice.

## Writing one

A new ADR takes the next number, with a name like `0014-short-title.md`. It starts with the front matter `status: accepted`, then a title, the decision, the options considered, and the consequences, as the existing ones do. If a new decision replaces an old one, remove the old ADR in the same pull request, and say so in the new one.
