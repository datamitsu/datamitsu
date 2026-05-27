Gem::Specification.new do |spec|
  spec.name          = "datamitsu"
  spec.version       = "0.0.0"
  spec.authors       = ["Alexander Svinarev"]
  spec.email         = ["shibanet0@gmail.com"]

  spec.summary       = "Configuration management and binary distribution tool for development tools"
  spec.description   = "datamitsu downloads, verifies (SHA-256), and manages binaries for linting and development tools across platforms using JavaScript-powered configuration."
  spec.homepage      = "https://github.com/datamitsu/datamitsu"
  spec.license       = "MIT"

  spec.metadata = {
    "bug_tracker_uri"   => "https://github.com/datamitsu/datamitsu/issues",
    "changelog_uri"     => "https://github.com/datamitsu/datamitsu/releases",
    "documentation_uri" => "https://datamitsu.com",
    "homepage_uri"      => "https://github.com/datamitsu/datamitsu",
    "source_code_uri"   => "https://github.com/datamitsu/datamitsu",
  }

  spec.bindir        = "bin"
  spec.executables   = ["datamitsu"]
  spec.require_paths = ["lib"]

  spec.files = %w(
    lib/datamitsu.rb
    bin/datamitsu
    README.md
  ) + Dir.glob("libexec/**/datamitsu{,.exe}")

  spec.post_install_message = <<~MSG
    datamitsu installed!
    Run 'datamitsu init' in your project to get started.
    Documentation: https://datamitsu.com
  MSG
end
