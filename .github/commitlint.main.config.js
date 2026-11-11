// CI-only config for .github/workflows/commitlint.yml. Same rules as the root
// config, but without commitlint's default ignores (Revert, fixup!, Merge).
// Local hooks need those for `git revert` and `--fixup`. main doesn't, because
// the repo allows only squash merges and the PR title check rejects them.
import base from '../commitlint.config.js';

export default { ...base, defaultIgnores: false };
