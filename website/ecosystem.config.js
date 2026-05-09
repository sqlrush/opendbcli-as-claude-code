module.exports = {
  apps: [
    {
      name: "opendb-website",
      script: "node_modules/.bin/next",
      args: "start -p 3000",
      cwd: "/opt/opendb-website/website",
      env: {
        NODE_ENV: "production",
      },
      instances: 1,
      autorestart: true,
      max_memory_restart: "256M",
    },
  ],
};
