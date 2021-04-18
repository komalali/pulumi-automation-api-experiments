import * as inquirer from "inquirer";
import * as React from "react";
import { render, Text } from "ink";

import { s3 } from "@pulumi/aws";
import { PolicyDocument } from "@pulumi/aws/iam";
import { InlineProgramArgs, LocalWorkspace } from "@pulumi/pulumi/automation";
import { Color } from "chalk";

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

interface Answers {
    destroy: boolean;
}

interface UpdateProps {
    destroy: boolean;
}

const Update = (props: UpdateProps) => {
    const [message, setMessage] = React.useState("");
    const [textColor, setColor] = React.useState("green");

    React.useEffect(() => {
        const runPulumiUpdate = async () => {
            try {
                setMessage("Creating stack...");
                const stack = await LocalWorkspace.createOrSelectStack(
                    stackArgs
                );

                setMessage("Ensuring plugins...");
                await stack.workspace.installPlugin("aws", "v3.38.1");

                setMessage("Setting configuration...");
                await stack.setConfig("aws:region", {
                    value: "us-west-2",
                });

                setMessage("Running refresh...");
                await stack.refresh();

                if (props.destroy) {
                    setMessage("Running destroy...");
                    await stack.destroy();

                    setMessage("Deleting stack...");
                    await stack.workspace.removeStack(stack.name);

                    setMessage("Success!");
                    return;
                }

                setMessage("Running update...");
                await stack.up();

                setMessage("Success!");
            } catch (error) {
                setColor("red");
                setMessage(`Failure!: ${error.error()}`);
            }
        };

        runPulumiUpdate();
    }, []);

    return <Text color={textColor}>Current step: {message}</Text>;
};

inquirer
    .prompt([
        {
            type: "list",
            name: "destroy",
            message: "What kind of update is this?",
            default: false,
            choices: [
                { name: "update", value: false },
                { name: "destroy", value: true },
            ],
        },
    ])
    .then((answers: Answers) => render(<Update destroy={answers.destroy} />))
    .catch(error => {
        if (error.isTtyError) {
            console.error(
                "Prompt couldn't be rendered in the current environment."
            );
        } else {
            console.error(error);
        }
    });
