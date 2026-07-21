# Task: AWS Infrastructure and GitOps Deployment Pipeline

We are hosting this monolithic Go web application on AWS. Create the declarative infrastructure files and the automated deployment workflow.

1. **Dockerfile**:
   - Write a multi-stage `Dockerfile` to build the compiled Go binary. The final image should copy the binary along with the `ui/templates/` and `ui/static/` directories.

2. **Terraform**:
   - Create a `main.tf` file to provision the necessary AWS resources.
   - Define an AWS ECR (Elastic Container Registry) to store the Docker image.
   - Configure AWS App Runner to pull the image from ECR and serve the web application over HTTPS. 
   - *Note for LLM:* Because App Runner containers are ephemeral, include a note or configuration block suggesting the migration of the database connection string from local SQLite to an Amazon RDS (PostgreSQL) instance for persistent state.

3. **GitHub Actions**:
   - Write a `.github/workflows/deploy.yml` file to handle automated GitOps deployments.
   - The workflow should trigger on a push to the `main` branch.
   - Steps must include: authenticating with AWS using OIDC, building the Docker image, pushing it to the ECR repository, and triggering an AWS App Runner service update.
