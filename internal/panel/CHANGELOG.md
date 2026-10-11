# Changelog

## [0.3.0](https://github.com/Jav1x/DNSmarty/compare/panel-v0.2.0...panel-v0.3.0) (2026-10-11)


### Features

* access page with per-rule 24h hits ([569501d](https://github.com/Jav1x/DNSmarty/commit/569501df0dc085a1d42c81c79eef9f4898dcf4d3))
* **auth:** TOTP sign-in with backup codes ([e3830e1](https://github.com/Jav1x/DNSmarty/commit/e3830e130dea3d226b3c41d58da176671e16af2b))
* **buildinfo:** add version normalization and agent update check ([5850d5b](https://github.com/Jav1x/DNSmarty/commit/5850d5b0c98b82b67bf489cc152ab2d798437ff1))
* **dns:** measure resolve latency per hit ([3da325f](https://github.com/Jav1x/DNSmarty/commit/3da325f12428b50b8679db9188aaca50a55dce72))
* OAuth, node rotation order, stats rewrite, English API errors ([f29ebed](https://github.com/Jav1x/DNSmarty/commit/f29ebed0e4b7ab7999681e2f03f5bda7bf265c5e))
* **panel:** services and templates API ([f9053d4](https://github.com/Jav1x/DNSmarty/commit/f9053d415a084ab9823da1fc6af6ae40ee62fa48))
* **panel:** store node hardware snapshot and expose it in /api/nodes ([0dac9db](https://github.com/Jav1x/DNSmarty/commit/0dac9db07d5066e3cba3f6a1994f213521280fb2))
* **web:** hardware state, DNS latency in logs and overview ([6357531](https://github.com/Jav1x/DNSmarty/commit/635753150df548fe2871f64ed35ea1f30a9f2953))
* **web:** Ice+ account page with password meter and sessions ([9cdfd78](https://github.com/Jav1x/DNSmarty/commit/9cdfd78bade0cdbe36236676bb424efd3c722893))
* **web:** Ice+ design system tokens, fonts, Radix wrappers ([239e7ad](https://github.com/Jav1x/DNSmarty/commit/239e7ad63558c8510b0f5b40fa742fc1ff47b04a))
* **web:** Ice+ logs and audit ([e9f6872](https://github.com/Jav1x/DNSmarty/commit/e9f6872b2e88699316ce3110a64493757e4f6975))
* **web:** Ice+ nodes page with disable switch ([315fa39](https://github.com/Jav1x/DNSmarty/commit/315fa39faf71c4005963b64811fd63e15aef8307))
* **web:** Ice+ overview hero band and board ([47493e1](https://github.com/Jav1x/DNSmarty/commit/47493e103d0df4732323ff0b6e8206076e4ca041))
* **web:** Ice+ settings with upstream proto chips ([8825c92](https://github.com/Jav1x/DNSmarty/commit/8825c92011549e61fc3b160e5c8a2b4aa1b5bb72))
* **web:** Ice+ shell, access/services routes with legacy redirects ([436d8c8](https://github.com/Jav1x/DNSmarty/commit/436d8c80632fabf360e0341509f9999bda1f20a1))
* **web:** Ice+ stats with client drill-down ([611813b](https://github.com/Jav1x/DNSmarty/commit/611813b661e4dc192c71a601e9da035ee2246fa4))
* **web:** new node wizard with connect polling ([6ca1447](https://github.com/Jav1x/DNSmarty/commit/6ca1447998f30a0b0e7667272ac5ef4219843edc))
* **web:** orbital login screen ([dfee864](https://github.com/Jav1x/DNSmarty/commit/dfee8642f5107ee238f8c07081ea445a6522459b))
* **web:** services page and templates; drop legacy domain store ([94972a8](https://github.com/Jav1x/DNSmarty/commit/94972a84003052978428d4f5c62535d8c04482e0))


### Bug Fixes

* **web:** access review fixes — type header, zero hits for silent rules, ru plural ([0c61b71](https://github.com/Jav1x/DNSmarty/commit/0c61b71d44304df316011ba8bcf7d42c9d0b7976))
* **web:** drop duplicate bulk disable and providers request before phase 2 ([060ffb0](https://github.com/Jav1x/DNSmarty/commit/060ffb0539b4674097cfa4f717954de8ddf47b7a))
* **web:** emit fonts as files so the strict CSP font-src self allows them ([2cd66ee](https://github.com/Jav1x/DNSmarty/commit/2cd66eeb3ddb9771ae2d6c9c3db03e7c229055df))
* **web:** logs load-more guards and honest filter scope ([f0d18c1](https://github.com/Jav1x/DNSmarty/commit/f0d18c1c3739aace8b8cc7c967a32ef332935bb2))
* **web:** nodes bulk count follows selection, delete toast ([c4c7ce0](https://github.com/Jav1x/DNSmarty/commit/c4c7ce0ddcfe9c7bc930dedc48288a7dad4cbe92))
* **web:** nodes review fixes — role-based port defaults, bulk reload, dead keys ([f85638b](https://github.com/Jav1x/DNSmarty/commit/f85638b778bd07e3f9f19c182b05f3af5ea2bbf7))
* **web:** rename settings chip column class to avoid .chip collision ([97d7d94](https://github.com/Jav1x/DNSmarty/commit/97d7d9495f2ff91b0fca18fcec0259224c238948))
* **web:** render modal content inside its backdrop ([7b6d829](https://github.com/Jav1x/DNSmarty/commit/7b6d829aee889fca3bba657b101bb2b6f9b5396d))
* **web:** review fixes — self-sufficient Tooltip provider, Radix to deps, dist stub restored ([96785cb](https://github.com/Jav1x/DNSmarty/commit/96785cbdd7c3db647fc732a7172152d47f89a296))
* **web:** sequential account section numbers and compact session dates ([ddc8f11](https://github.com/Jav1x/DNSmarty/commit/ddc8f11ae5d4e5098af9e367482c405f5f121dec))
* **web:** settings — api import, DoT chip style, empty rows, inline styles ([82f4d36](https://github.com/Jav1x/DNSmarty/commit/82f4d3683040acc8706b2f1dc5f763815d48798b))
* **web:** stats shows skeleton while window or client refetches ([56b0df4](https://github.com/Jav1x/DNSmarty/commit/56b0df43f45aaf3eadc69f8ec1f80359221ddc6f))
* **web:** style the service domain field and pluralize counters ([93be864](https://github.com/Jav1x/DNSmarty/commit/93be8641c9249ab84772bfd1ffba75f37bb9851b))
