pub fn value() -> u32 { base::value() + if cfg!(feature = "extra") { 2 } else { 1 } }
