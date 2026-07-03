// The App Group container is where the Go bridge stores its "shared" root
// directory on iOS (see RootDir.swift). Every build variant that is installed
// side-by-side on a device MUST use its own container, otherwise a debug build
// sees the production account list but not its data (ErrBertyAccountDataNotFound)
// because the app-private "Documents" root is not shared while the App Group is.
//
// This mirrors the historical native scheme (group.tech.berty for release,
// group.tech.berty.dev for debug, ...) by deriving the suffix from the bundle
// identifier variant (tech.berty.ios, tech.berty.ios.debug, tech.berty.ios.staff).

export const APP_GROUP_PREFIX = "group.tech.berty";
export const PRODUCTION_BUNDLE_IDENTIFIER = "tech.berty.ios";

export const getAppGroupID = (bundleIdentifier?: string): string => {
	if (!bundleIdentifier || bundleIdentifier === PRODUCTION_BUNDLE_IDENTIFIER) {
		return APP_GROUP_PREFIX;
	}

	// Variant bundle ids extend the production one (e.g. "tech.berty.ios.debug"),
	// so reuse that ".debug"/".staff" suffix to keep the container name readable.
	if (bundleIdentifier.startsWith(`${PRODUCTION_BUNDLE_IDENTIFIER}.`)) {
		const suffix = bundleIdentifier.slice(PRODUCTION_BUNDLE_IDENTIFIER.length);
		return `${APP_GROUP_PREFIX}${suffix}`;
	}

	// Fallback for unexpected bundle ids: keep uniqueness by appending the whole id.
	return `${APP_GROUP_PREFIX}.${bundleIdentifier}`;
};
