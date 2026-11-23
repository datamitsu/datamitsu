require "rubygems/package"

path, version = ARGV
spec = Gem::Package.new(path).spec
unless spec.name == "datamitsu" && spec.version.to_s == version.sub("-rc.", ".rc.") &&
       spec.platform.to_s == "ruby" && spec.bindir == "bin" && spec.executables == ["datamitsu"]
  abort "Gem specification does not match the approved release and CLI declaration"
end
