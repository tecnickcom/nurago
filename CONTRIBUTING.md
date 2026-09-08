# How to Contribute

The [development guide](https://nurago.org/docs/development/) describes the Makefile workflow, linting, coverage, and benchmarks.

## Add a new feature or change an existing one

* Before writing any code please suggest the change by opening a new Feature Request [issue](https://github.com/tecnickcom/nurago/issues/new).

## Reporting a Bug

* **Do not open up a GitHub issue if the bug is a security vulnerability**, and instead refer to our [Security policy](SECURITY.md).

* Ensure the bug was not already reported by searching on GitHub under [Issues](https://github.com/tecnickcom/nurago/issues).

* If you're unable to find an open issue addressing the problem, [open a new one](https://github.com/tecnickcom/nurago/issues/new). Be sure to include a **title and clear description**, as much relevant information as possible, and a **code sample** or an **executable test case** demonstrating the expected behavior that is not occurring.

## Submitting a Change (bug fix or feature)

* Open a new GitHub pull request (PR).
* Ensure the PR description clearly outlines both the problem and the proposed solution. Reference the relevant issue number where applicable.
* Adhere to the existing coding conventions throughout your changes.
* For bug fixes, include new tests that cover the specific cases being addressed.
* Maintain unit test coverage at 100%.
* For new features, provide comprehensive documentation for all packages and functions, and include examples and benchmarks where appropriate.
* Before submitting your PR, run `make x` locally to verify that the code passes all checks.
