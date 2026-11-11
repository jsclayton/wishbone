// Conventional commits. Used by the commit-msg hook (lefthook.yml) and by
// .github/workflows/commitlint.yml on main (through
// .github/commitlint.main.config.js). Keep in step with the PR title check in
// .github/workflows/ci.yml. Its subjectPattern `^(?![A-Z]).+$` is mirrored by
// the local subject-no-leading-uppercase rule, so subject-case is off.
export default {
  extends: ['@commitlint/config-conventional'],
  plugins: [
    {
      rules: {
        'no-session-link': ({ raw }) => [
          !/^Claude-Session:|claude\.ai\/code\/session/im.test(raw ?? ''),
          'remove the Claude-Session: line and any session link',
        ],
        'subject-no-leading-uppercase': ({ subject }) => [
          subject == null || !/^[A-Z]/.test(subject),
          'subject must not start with an uppercase letter',
        ],
      },
    },
  ],
  rules: {
    'type-enum': [
      2,
      'always',
      ['feat', 'fix', 'docs', 'style', 'refactor', 'perf', 'test', 'build', 'ci', 'chore', 'revert'],
    ],
    'subject-case': [0],
    'subject-no-leading-uppercase': [2, 'always'],
    'no-session-link': [2, 'always'],
  },
};
