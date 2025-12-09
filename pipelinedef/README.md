# Pipeline definition in Konflux

While our main pipelines are building from the default branches of our various
repositories, we may need to create separate working branches from time to time,
and want those branches to have their own CI pipelines to be able to build and
release the corresponding code.

This folder and the files it contains can be used to duplicate our default
pipelines in Konflux, in order to manage such concurrent work.

It is triggered by the need to work on a 1.10.2 release for specific need, while
continuing the longer-term 1.11 work. We will keep the files in this folder
for future reference in case we need to do it again.

This follows the [Konflux documentation](https://konflux.pages.redhat.com/docs/users/patterns/managing-multiple-versions.html) for managing multiple versions of a product.

## High-level process

- define our pipelines in Konflux using the usual onboarding method
  
  This step is DONE for us, as can be seen in our application's definition in
  our [Konflux instance](https://konflux-ui.apps.stone-prd-rh01.pg1f.p1.openshiftapps.com/ns/ose-osc-tenant/applications/openshift-sandboxed-containers)

- prepare the project.yaml file
  
  This defines our `Project` for the project controller.
  The file we have in this folder is a copy of the documentation's example,
  modified with our project's name.

- prepare the template.yaml file
  
  It defines the `ProjectDevelopmentStreamTemplate` for our project.
  
  This is the most important file, as it defines what our pipeline looks like.
  It contains the definition for:
  - applications
  - components
  - image repositories
  - integration tests
  
  All those definitions must have parameterized names, so that we can use it to
  create separate pipelines by giving it a different parameter (version typically).

- prepare the devstream.yaml file
  
  It defines the `ProjectDevelopmentStream`, which is the representation of one
  instance of our pipelines.
  
  This is the file that triggers the actual creation of the pipeline, using the
  provided version as parameter.

- apply the yaml files in order

- create the branch in the various repositories.

- create the ReleasePlan/ReleasePlanAdmission (can be done at a later time)

> **Note:**
>
> - Once the `Project` and `ProjectDevelopmentStreamTemplate` are set in
> our Konflux instance, we can just do the creation step as many times as needed.
> However, ensure that  the existing template is up to date with our latest "main
> pipeline" definition, as it may evolve over time.
> - The `ProjectDevelopmentStream` object controls the creation and
> deletion of the pipeline. Trying to delete the Applciations/Components/etc
> from the Konflux UI or CLI is useless: they will be re-created by the project
> manager.

## Prerequisite

- Have a CLI access to the konflux instance we use.
  See [documentation](https://konflux.pages.redhat.com/docs/users/getting-started/getting-access-new.html)
- Make sure you're using our namespace (ose-osc-tenant)

## template.yaml

The file contains the definition of our pipeline structure.
In order to have a full build of Sandboxed Containers, we need:

- the "openshift-sandboxed-containers" application, and all its components
- the "osc-test-catalog" application, and the associated "osc-test-fbc" component
- the integration tests associated with those two applications.
  Note that the Enterprise Contract doesn't need to be in the template, as it is
  created automatically for all components in Konflux.
- a ReleasePlan allowing the release of the images built with the new pipeline

For each of them, we extract the yaml definition found on the Konflux instance:

```bash
$ oc get application openshift-sandboxed-containers -o yaml > osc-app.yaml
$ oc get component osc-operator -o yaml > osc-operator-comp.yaml
$ ...
```

The resulting yaml files need to be edited (some lines removed, others edited)
following the steps from the documentation.

To keep things consistent, we are keeping the names of applications and components,
and just add the version as a suffix.
e.g: "osc-operator-bundle-{{.versionName}}"

We're also going to assume that we use the same branch name for all repositories,
in the format "osc-release-{{.version}}".

This way, when we prepare the devstream.yaml file, we can provide the version
we need (e.g: "v1.10") to create the pipelines that will build from the
`osc-release-v1.10` branch of all our repositories.

### Note about versions

We have two parameters in the pipeline def (see "variables" in template.yaml):

- .version
- .versionName

The first is the one we provide when we create a new stream.
The second is set by default to a "hyphenized" version.
E.g: when we set version to "v1.10", we get "v1-10" for versionName.

The version name is meant to be used in kubernetes objects, where dot is not allowed.
We're using it everywhere in the template to make sure the objects we create are
unique, with their name based on the provided version.

For the git branch though, we can use the "dot" character, and we probably prefer
to use that rather than the hyphenized version.

### About the build-dm-verity-image-task

We have that application and component that is used to create the build task
we use for building the dm-verity image.
This task is not duplicated for now, as we expect the same task can be used
for multiple versions of the dm-verity build.

If we require specific versions of the tasks for specific versions of dm-verity,
we may need to duplicate that too.

>[NOTE (Julien - 2025/08/19)] the existing pipeline define a nudge from
`osc-podvm-payload` to the `build-dm-verity-image-task`. But the resulting PRs
are changing the `osc-podvm-payload` image reference in the `build-pipeline.yaml`
file used by the `osc-dm-verity-image` component. And only that component gets rebuilt with it.
>
>I think the right nudge should target the `osc-dm-verity-image` component, with
the same result as `build-dm-verity-image-task` and `osc-dm-verity-image` share
the same repository.
>
>If this nudge really needs to happen, we'll need to duplicate the build task too.

>[NOTE (Julien - 2025/12/09)] the experience from the 1.10.x releases is that we
>need to get the build-dm-verity-image-task duplicated too. The reason for that
>is that otherwise, if the task diverge because of changes in the next dev cycle
>the previous branch for dm-verity can't be built anymore, as the task doesn't
>stay available.
>Having two tasks and publishing them separately should help with that.
>Now the problem is that they also need to be versioned, otherwise only one task
>image will be kept (I think?).
>This may require more info / help from the Konflux support.

## Branching

We need to make the branch with a version in its name, so that the pipelines can
be differenciated.

We also need to modify the the branch's `.tekton` files to make sure it has the
same application' and component's names, otherwise the pipelines won't find their
settings and won't be able to trigger.

We can create the branch after the pipelines are created. And we should make a PR
after it is created to validate the pipelines.
The documentation recommends that this first PR contains the required .tekton file
modifications.

We need to add some changes of our own in this first PR, or right after:

- update the kata-containers and guest-component submodules in cloud-api-adaptor,
  to point to the HEAD of the new branch
- update the cloud-api-adaptor submodule in sandboxed-containers, to point to the
  HEAD of the new branch (after it's been updated with the right submodules)

We also need to pay attention to the nudge PRs from podvm-payload to dm-verity,
from all components to the bundle, and from the bundle to the test-fbc.
Make sure the PRs are all created as expected, for the right component on the
right branch.

## ReleasePlan / ReleasePlanAdmission

All of the previous steps allows to build and test from the new branch.
To be able to release anything, we need to set the ReleasePlan and ReleasePlanAdmission

This is basically the same as documented:

- https://konflux.pages.redhat.com/docs/users/releasing/index.html
- https://konflux.pages.redhat.com/docs/users/releasing/preparing-for-release.html

But we can simplify it by using the same ReleasePlanAdmission, depending on
what we want to release, and when.

As long as we don't need to support different versions of the product at the same
time, we don't need a separate ReleasePlanAdmission.

At the time of creating these files, our goal is to release 1.10.2, then release 1.11.
When we release 1.11, 1.10.2 will not be supported anymore, so we can keep using
the same ReleasePlanAdmission.

If we move to a multi-stream scenario and use the template in this folder to
create the pipelines, we will need a separate RPA.

When keeping the same RPA, we just need to edit the existing one with the following:

- add the new application to the list of applications in the RPA
  (e.g: "openshift-sandboxed-containers-v1-10)
- add the new components to the list of components in the RPA
