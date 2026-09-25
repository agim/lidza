//! Līdza compute core.
//!
//! Phase 1 ships the crate and its toolchain pin only. The Go binder
//! (`pkg/engine`, wazero) and the first exports arrive with the packs in
//! roadmap Phase 4.

/// The crate version, as a NUL-free ASCII string.
pub fn version() -> &'static str {
    env!("CARGO_PKG_VERSION")
}

/// Exported for the WASM targets so a module built from this crate has at
/// least one callable symbol to verify the toolchain with.
#[unsafe(no_mangle)]
pub extern "C" fn lidza_core_abi_version() -> u32 {
    1
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn version_matches_manifest() {
        assert_eq!(version(), "0.1.0");
    }

    #[test]
    fn abi_version() {
        assert_eq!(lidza_core_abi_version(), 1);
    }
}
