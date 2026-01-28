const { verifyAuth } = require("daku");
const { ALLOWED_USERS } = require("./config");

const authMiddleware = async (req, res, next) => {
    try {
        const authHeader = req.headers.authorization;
        if (!authHeader || !authHeader.startsWith("Bearer ")) {
            return res.status(401).json({ error: "Missing or invalid authorization header" });
        }

        const token = authHeader.split(" ")[1];
        const userId = await verifyAuth(token);

        if (!userId) {
            return res.status(401).json({ error: "Invalid token" });
        }

        // Check Whitelist if configured
        if (ALLOWED_USERS.length > 0 && !ALLOWED_USERS.includes(userId)) {
            return res.status(403).json({ error: "User not authorized (whitelist enforced)" });
        }

        req.user = { id: userId };
        next();
    } catch (err) {
        console.error("Auth error:", err);
        res.status(500).json({ error: "Internal server error during authentication" });
    }
};

module.exports = { authMiddleware };
