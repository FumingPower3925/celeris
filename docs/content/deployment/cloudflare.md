---
title: "Cloudflare Pages Deployment"
weight: 1
---

# Deploying to Cloudflare Pages

This guide explains how to deploy the Celeris documentation to Cloudflare Pages.

## Prerequisites

- A Cloudflare account
- GitHub repository at `https://github.com/FumingPower3925/celeris`
- Hugo installed locally for testing

## Automatic Deployment (Recommended)

The repository is configured with GitHub Actions for automatic deployment.

### Setup GitHub Secrets

Add these secrets to your GitHub repository settings:

1. **CLOUDFLARE_API_TOKEN**
   - Go to Cloudflare Dashboard → My Profile → API Tokens
   - Create a token with "Cloudflare Pages" permissions
   
2. **CLOUDFLARE_ACCOUNT_ID**
   - Found in Cloudflare Dashboard → Account Home
   - Copy your Account ID

### Workflow

The `.github/workflows/deploy-docs.yml` workflow:

- Triggers on push to `main` branch (when docs/ changes)
- Builds Hugo site with `hugo --minify`
- Deploys to Cloudflare Pages project `celeris-docs`

## Manual Deployment

### Option 1: Direct Upload

1. Build the documentation locally:
   ```bash
   cd docs
   hugo --minify
   ```

2. Go to [Cloudflare Pages Dashboard](https://dash.cloudflare.com/pages)

3. Click "Create a project" → "Direct Upload"

4. Upload the `docs/public` directory

### Option 2: Git Integration

1. Go to Cloudflare Pages Dashboard

2. Click "Create a project" → "Connect to Git"

3. Connect your GitHub account and select the repository

4. Configure build settings:
   - **Build command**: `cd docs && hugo --minify`
   - **Build output directory**: `docs/public`
   - **Root directory**: `/`
   
5. Click "Save and Deploy"

## Build Settings

### Environment Variables

No environment variables are required for the basic setup.

### Hugo Version

The workflow uses the latest Hugo extended version. To specify a version:

```yaml
- name: Setup Hugo
  uses: peaceiris/actions-hugo@v2
  with:
    hugo-version: '0.120.0'
    extended: true
```

## Custom Domain (Optional)

To use a custom domain:

1. Go to your Cloudflare Pages project
2. Click "Custom domains"
3. Add your domain
4. Update DNS records as instructed

## Accessing Your Site

After deployment, your site will be available at:

- **Production**: `https://celeris-docs.pages.dev`
- **Custom domain** (if configured): `https://docs.celeris.dev`

## Preview Deployments

Cloudflare Pages automatically creates preview deployments for:

- Pull requests
- Non-main branch pushes

Preview URLs follow the pattern:
```
https://[hash].celeris-docs.pages.dev
```

## Troubleshooting

### Build Failures

**Hugo version mismatch:**
```
Solution: Update hugo-version in the workflow
```

**Theme not found:**
```bash
# Ensure git submodules are initialized
git submodule update --init --recursive
```

**Build command fails:**
```yaml
# Verify the build command in .github/workflows/deploy-docs.yml
build-command: cd docs && hugo --minify
```

### Deployment Failures

**API Token invalid:**
- Regenerate the token in Cloudflare
- Update GitHub secret

**Account ID wrong:**
- Double-check your Cloudflare Account ID
- Update GitHub secret

## Local Testing

Test the build locally before deploying:

```bash
cd docs
hugo server -D

# Test production build
hugo --minify
cd public && python3 -m http.server 8000
```

Visit `http://localhost:8000` to preview.

## CI/CD Pipeline

The complete CI/CD pipeline:

1. **Push to main** → Triggers workflow
2. **Checkout** → Clone repository with submodules
3. **Setup Hugo** → Install Hugo extended
4. **Build** → Run `hugo --minify`
5. **Deploy** → Upload to Cloudflare Pages
6. **Live** → Site updated at `celeris-docs.pages.dev`

## Performance Optimization

Cloudflare Pages automatically provides:

- Global CDN distribution
- HTTPS/SSL certificates
- HTTP/2 and HTTP/3 support
- Automatic image optimization
- Brotli compression

## Monitoring

View deployment logs:

1. Go to Cloudflare Pages Dashboard
2. Click on your project
3. View "Deployments" tab
4. Click on any deployment to see logs

## Rollback

To rollback to a previous version:

1. Go to Deployments tab
2. Find the working deployment
3. Click "..." → "Rollback to this deployment"

## Additional Resources

- [Cloudflare Pages Documentation](https://developers.cloudflare.com/pages/)
- [Hugo on Cloudflare Pages](https://developers.cloudflare.com/pages/framework-guides/deploy-a-hugo-site/)
- [GitHub Actions for Cloudflare Pages](https://github.com/cloudflare/pages-action)

