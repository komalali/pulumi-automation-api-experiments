import * as inquirer from "inquirer";
import * as Listr from "listr";
import * as chalk from "chalk";
import * as rxjs from "rxjs";

import { s3 } from "@pulumi/aws";
import { PolicyDocument } from "@pulumi/aws/iam";
import {
    InlineProgramArgs,
    LocalWorkspace,
    Stack,
} from "@pulumi/pulumi/automation";

// This is our pulumi program in "inline function" form
const pulumiProgram = async () => {
    // Create a bucket and expose a website index document
    const siteBucket = new s3.Bucket("s3-website-bucket", {
        website: {
            indexDocument: "index.html",
        },
    });
    const indexContent = `<html><head>
<title>Hello S3</title><meta charset="UTF-8">
</head>
<body><p>Hello, world!</p><p>Made with ❤️ with <a href="https://pulumi.com">Pulumi</a></p>
</body></html>
`;

    // write our index.html into the site bucket
    new s3.BucketObject("index", {
        bucket: siteBucket,
        content: indexContent,
        contentType: "text/html; charset=utf-8",
        key: "index.html",
    });

    // Create an S3 Bucket Policy to allow public read of all objects in bucket
    function publicReadPolicyForBucket(bucketName: string): PolicyDocument {
        return {
            Version: "2012-10-17",
            Statement: [
                {
                    Effect: "Allow",
                    Principal: "*",
                    Action: ["s3:GetObject"],
                    Resource: [
                        `arn:aws:s3:::${bucketName}/*`, // policy refers to bucket name explicitly
                    ],
                },
            ],
        };
    }

    // Set the access policy for the bucket so all objects are readable
    new s3.BucketPolicy("bucketPolicy", {
        bucket: siteBucket.bucket, // refer to the bucket created earlier
        policy: siteBucket.bucket.apply(publicReadPolicyForBucket), // use output property `siteBucket.bucket`
    });

    return {
        websiteUrl: siteBucket.websiteEndpoint,
    };
};

const stackArgs: InlineProgramArgs = {
    stackName: "dev",
    projectName: "inlineNode",
    program: pulumiProgram,
};

interface AppContext {
    destroy: boolean;
    stack?: Stack;
}

const tasks = new Listr([
    {
        title: "Running update",
        task: (_, task) =>
            new Listr([
                {
                    title: "Creating stack",
                    task: async (ctx: AppContext) => {
                        ctx.stack = await LocalWorkspace.createOrSelectStack(
                            stackArgs
                        );
                    },
                },
                {
                    title: "Ensuring plugins",
                    task: async (ctx: AppContext) =>
                        await ctx.stack.workspace.installPlugin(
                            "aws",
                            "v3.38.1"
                        ),
                },
                {
                    title: "Setting config",
                    task: async (ctx: AppContext) => {
                        await ctx.stack.setConfig("aws:region", {
                            value: "us-west-2",
                        });
                    },
                },
                {
                    title: "Refreshing stack",
                    task: async (ctx: AppContext) => {
                        await ctx.stack.refresh();
                    },
                },
                {
                    title: "Updating stack",
                    enabled: (ctx: AppContext) => !ctx.destroy,
                    task: async ctx => {
                        await ctx.stack.up();
                        task.title = "Update complete";
                    },
                },
                {
                    title: "Destroying stack",
                    enabled: (ctx: AppContext) => ctx.destroy,
                    task: async ctx => {
                        await ctx.stack.destroy();
                        task.title = "Update complete";
                    },
                },
            ]),
    },
]);

inquirer
    .prompt([
        {
            type: "list",
            name: "typeOfUpdate",
            message: "What kind of update is this?",
            default: false,
            choices: [
                { name: "update", value: false },
                { name: "destroy", value: true },
            ],
        },
    ])
    .then(answers =>
        tasks.run({ destroy: answers.typeOfUpdate }).then(async ctx => {
            if (!ctx.destroy) {
                const outputs = await ctx.stack.outputs();
                console.log(
                    `\n${chalk.underline.blue("Website URL")}: http://${
                        outputs.websiteUrl.value
                    }\n`
                );
            }
        })
    )
    .catch(error => {
        if (error.isTtyError) {
            console.error(
                "Prompt couldn't be rendered in the current environment."
            );
        } else {
            console.error(error);
        }
    });
