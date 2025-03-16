# Architecture

The heart of this project is a state machine that turns stuff in GitHub into a running app. 

The stages are: 

1. Trigger
2. Build (skip if existing build) 
3. Deploy (skip if existing deploy) 

Surrounding these is managing the ecosystem that allows the apps to run happily. 

1. Install skyline
2. Prep VM 
3. Monitor VM 
4. Monitor apps 
5. Reverse proxy to apps, manage SSL
6. Manage and backup databases 
7. Manage S3 credentials
8. Manage database credentials
9. Logging, monitoring, and tracing

## What has been built so far? 

Scaffolding to allow apps to be created. We have build and deploy, but not skip if it exists. Should that be the database or something else? 

What's the minimum for me to be able to ship this? 

- [ ] Running on a VM 
- [ ] Do not do the VM setup yet
- [ ] Reverse proxying to two apps based on subdomain 
- [ ] DO not worry about security yet
- [ ] Actually do not worry about ssl yet
- [ ] Just running on a VM and proxying to a few other apps (go based) 

