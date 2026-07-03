import { ConfigPlugin, withEntitlementsPlist } from "@expo/config-plugins";

import { getAppGroupID } from "./appGroup";

const withIosEntitlements: ConfigPlugin = (config) => {
	return withEntitlementsPlist(config, (config) => {
		if (config.ios?.bundleIdentifier === "tech.berty.ios") {
			config.modResults["aps-environment"] = "production";
		} else {
			config.modResults["aps-environment"] = "development";
		}

		config.modResults["com.apple.developer.associated-domains"] = [
			"applinks:berty.tech",
		];
		config.modResults["com.apple.security.application-groups"] = [
			getAppGroupID(config.ios?.bundleIdentifier),
		];
		config.modResults["keychain-access-groups"] = [
			"$(AppIdentifierPrefix)tech.berty.ios",
		];
		config.modResults["com.apple.developer.usernotifications.filtering"] = true;
		return config;
	});
};

export default withIosEntitlements;
