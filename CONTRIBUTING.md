## Contributing to Celeris

Thank you for your interest in contributing to Celeris! This document provides guidelines for contributing to the project.

## Code of Conduct

This project adheres to a code of conduct. By participating, you are expected to uphold this code. Please be respectful and constructive in all interactions.

## How to Contribute

### Reporting Bugs

Before creating bug reports, please check the issue list as you might find that you don't need to create one. When you are creating a bug report, please include as many details as possible:

* Use a clear and descriptive title
* Describe the exact steps to reproduce the problem
* Provide specific examples to demonstrate the steps
* Describe the behavior you observed and what behavior you expected
* Include logs, error messages, or screenshots if applicable
* Specify your environment (OS, Go version, etc.)

### Suggesting Enhancements

Enhancement suggestions are tracked as GitHub issues. When creating an enhancement suggestion, please include:

* A clear and descriptive title
* A detailed description of the proposed enhancement
* Explain why this enhancement would be useful
* List any alternative solutions or features you've considered

### Pull Requests

1. Fork the repository
2. Create a new branch from `main` for your feature or bug fix
3. Make your changes
4. Add or update tests as needed
5. Ensure all tests pass
6. Update documentation if needed
7. Commit your changes with clear commit messages
8. Push to your fork and submit a pull request

#### Pull Request Guidelines

* Follow the Go coding style and conventions
* Write clear, concise commit messages
* Include tests for new functionality
* Update documentation for API changes
* Ensure CI passes
* Keep pull requests focused on a single feature or bug fix

## Development Setup

### Prerequisites

* Go 1.22 or later
* golangci-lint
* Hugo (for documentation)
* h2load (for load testing)
* h2spec (for compliance testing)

### Setup

```bash
# Clone your fork
git clone https://github.com/YOUR_USERNAME/celeris.git
cd celeris

# Install dependencies
go mod download

# Install development tools
make install-tools

# Run tests
make test

# Run linter
make lint
```

### Running Tests

```bash
# Run all tests
make test

# Run with coverage
make coverage

# Run benchmarks
make bench

# Run load tests
make load-test

# Run HTTP/2 compliance tests
make h2spec
```

### Code Style

* Follow standard Go conventions
* Use `gofmt` to format code
* Use meaningful variable and function names
* Add comments for exported functions and types
* Keep functions small and focused
* Write unit tests for new code

### Commit Messages

* Use the present tense ("Add feature" not "Added feature")
* Use the imperative mood ("Move cursor to..." not "Moves cursor to...")
* Limit the first line to 72 characters or less
* Reference issues and pull requests after the first line

Example:
```
Add support for server push

Implement HTTP/2 server push functionality to allow
pushing resources to clients before they request them.

Fixes #123
```

### Documentation

* Update documentation when adding features
* Keep README.md up to date
* Add examples for new functionality
* Document breaking changes

### Testing Guidelines

* Write tests for all new functionality
* Maintain or improve code coverage
* Test edge cases and error conditions
* Use table-driven tests when appropriate
* Benchmark performance-critical code

## Project Structure

```
celeris/
├── cmd/                    # Example applications
├── pkg/celeris/           # Public API
├── internal/              # Internal implementation
│   ├── transport/         # gnet integration
│   ├── frame/             # HTTP/2 frames
│   └── stream/            # Stream management
├── docs/                  # Documentation
└── .github/workflows/     # CI/CD
```

## Release Process

1. Update version in relevant files
2. Update CHANGELOG.md
3. Create a new tag
4. Push the tag to GitHub
5. GitHub Actions will handle the release

## Questions?

Feel free to open an issue for questions or join our discussions.

## License

By contributing to Celeris, you agree that your contributions will be licensed under the MIT License.

