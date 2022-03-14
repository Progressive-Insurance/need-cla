# How to Contribute

> NOTE: Currently, you **can't contribute** (unless you're a Progressive employee). We are in the process of finalizing our CLA (ironic) and cannot legally
> accept contributions at this time. We are very close to completing our automated CLA workflow, so please check back soon if you're looking to contribute.

We warmly welcome any community contributions to this repository.

## Code of Conduct

Help us ensure an inspiring, inclusive community.
Please read our [Code of Conduct](./CODE_OF_CONDUCT.md).

## Found a bug?

If you've discovered a bug you can [submit an issue](https://github.com/progressive-insurance/need-cla/issues), or skip straight to [creating a pull request](#submitting-a-pr) if you already have a fix.

## Want a new feature?

We can't wait to hear about your new ideas.
Please consider the size of the feature you're proposing before taking your next steps.

**Small** - You can [submit an issue](https://github.com/progressive-insurance/need-cla/issues) or just [create a pull request](#submitting-a-pr) if your feature is already implemented.

**Large** - Please [detail an issue](https://github.com/progressive-insurance/need-cla/issues) so that it can be discussed.
This gives us a chance to make sure we can coordinate the changes and helps ensure the easiest path forward for your changes.

## Submitting a PR

1. Check for open PRs with duplicate work.
2. [Sign our CLA](#signing-the-cla). This is **required** for us to accept your changes.
3. [Fork](https://docs.github.com/en/get-started/quickstart/fork-a-repo) the repo and make your changes in a new branch.

- If adding a new feature, create a `feature/*` branch. Don't forget to add new tests!
- If fixing a bug, create a `bug/*` branch.
- If updating documentation, create a `docs/*` branch.
- Use descriptive commit messages

4. Ensure all tests pass when running `go test ./...` from the root of your forked repository.
5. Rebase your branch to our `main` and push

```
git remote add upstream git@github.com:progressive-insurance/need-cla.git
git fetch upstream
git rebase upstream/main
git push --force-with-lease
```

6. [Create a pull request](https://docs.github.com/en/pull-requests/collaborating-with-pull-requests/proposing-changes-to-your-work-with-pull-requests/creating-a-pull-request-from-a-fork) to `need-cla:main`, responding to questions/feedback on the PR until it is merged.

## Signing the CLA

> NOTE: We are in the process of finalizing our CLA workflow.
> If you are attempting to contribute, please check back soon (or open an issue prodding us along!).

A signed Contributor License Agreement is required before we can accept any code from you.

## Maintainers

This repository is maintained by:

- Justin Tout (JUSTINTOUT)
