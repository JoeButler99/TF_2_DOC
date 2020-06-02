# TF_2_DOC

This program takes a terraform path and a template and can generate automatic documentation in markdown format.

It also produces links in the GitLab format.

    Usage of ./TF_2_DOC:
      -action string
            The Action to perform. [VarsTable OutputsTable ManagedResourcesTable DataSourcesTable RenderTemplate]
      -modulePath string
            The path of the module relative to the repository
      -path string
            The path to the Terraform Module to inspect.
      -repoUrl string
            The URL path used as a prefix for links
      -templatePath string
            The path to the template to render

You can also use this outside of template to render markdown tables for various Terraform object types.
